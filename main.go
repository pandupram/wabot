package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/api/option"
)

var (
	GenAIClient     *genai.Client
	WhatsmeowClient *whatsmeow.Client
	mutex           sync.Mutex
)

func main() {
	WhatsmeowClient = CreateClient()
	ConnectClient(WhatsmeowClient)
	WhatsmeowClient.AddEventHandler(HandleEvent)
	GenAIClient, _ = GetGenAIClient()

	defer GenAIClient.Close()

	WhatsmeowClient.Connect()
	// Listen to Ctrl+C (you can also do something else that prevents the program from exiting)
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	WhatsmeowClient.Disconnect()
}

func CreateClient() *whatsmeow.Client {
	dbLog := waLog.Stdout("Database", "INFO", true)
	container, err := sqlstore.New("sqlite3", "file:mydatabase.db?_foreign_keys=on", dbLog)
	if err != nil {
		log.Fatalln(err)
	}

	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		log.Fatalln(err)
	}

	clientLog := waLog.Stdout("Client", "INFO", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)

	return client
}

func ConnectClient(client *whatsmeow.Client) {
	if client.Store.ID == nil {
		// No ID stored, new login, show a qr code
		qrChan, _ := client.GetQRChannel(context.Background())
		err := client.Connect()
		if err != nil {
			log.Fatalln(err)
		}

		for evt := range qrChan {
			if evt.Event == "code" {
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				log.Println("Login event:", evt.Event)
			}
		}
	} else {
		// Already logged in, just connect
		err := client.Connect()
		if err != nil {
			log.Fatalln(err)
		}
	}
}

func HandleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		go HandleMessage(v)
	}
}

func HandleMessage(messageEvent *events.Message) {
	messageContent := messageEvent.Message.GetConversation()

	model := GenAIClient.GenerativeModel("gemini-pro")

	resp, err := model.GenerateContent(context.Background(), genai.Text(messageContent))
	if err != nil {
		return
	}

	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			result := fmt.Sprintf("%s", part)
			WhatsmeowClient.SendMessage(context.Background(), messageEvent.Info.Chat, &waProto.Message{
				Conversation: &result,
			})
		}
	}
}

func GetGenAIClient() (*genai.Client, error) {
	err := godotenv.Load()
	if err != nil {
		return nil, err
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	client, err := genai.NewClient(context.Background(), option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}
	return client, nil
}
