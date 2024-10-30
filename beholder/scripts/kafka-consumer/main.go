package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type LogTriggerMsg struct {
	Test types.Sequence `json:"test"`
}

func main() {
	kafkaBrokerUrl := os.Getenv("KAFKA_BROKER_URL")
	if kafkaBrokerUrl == "" {
		kafkaBrokerUrl = "localhost:19092"
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{kafkaBrokerUrl},
		Topic:    "beholder_otlp_logs",
		GroupID:  "my-group-1",
		MinBytes: 10e3,
		MaxBytes: 10e6,
	})
	defer reader.Close()

	for {
		msg, err := reader.ReadMessage(context.Background())
		if err != nil {
			log.Println("Error while reading message:", err)
			continue
		}

		var message Message
		if err := json.Unmarshal(msg.Value, &message); err != nil {
			log.Println("Error while unmarshalling message:", err)
			continue
		}

		for _, resourceLog := range message.ResourceLogs {
			for _, scopeLog := range resourceLog.ScopeLogs {
				for _, record := range scopeLog.LogRecords {
					data, err := base64.StdEncoding.DecodeString(record.Body.BytesValue)
					if err != nil {
						log.Println("Failed to decode Base64", err)
						continue
					}

					pbMap := new(pb.Map)
					if err := proto.Unmarshal(data, pbMap); err != nil {
						log.Println("Error while unmarshalling body:", err)
						continue
					}

					value, err := values.FromMapValueProto(pbMap)
					if err != nil {
						log.Println("Error creating values.Value object from pb.Value:", err)
						continue
					}
					var logTriggerMsg any
					err = value.UnwrapTo(&logTriggerMsg)
					if err != nil {
						log.Println("Error unwrapping to LogTriggerMsg type:", err)
						continue
					}

					jsonMap, err := json.Marshal(logTriggerMsg)
					if err != nil {
						log.Println("Failed to encode map", err)
						continue
					}

					fmt.Println(string(jsonMap))
				}
			}
		}
	}
}
