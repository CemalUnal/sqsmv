package main

import (
  "flag"
  "log"
  "os"
  "sync"

  "github.com/aws/aws-sdk-go/aws"
  "github.com/aws/aws-sdk-go/aws/session"
  "github.com/aws/aws-sdk-go/service/sqs"
)

func main() {
  src := flag.String("src", "", "source queue")
  dest := flag.String("dest", "", "destination queue")
  srcRegion := flag.String("src-region", "", "source queue region")
  destRegion := flag.String("dest-region", "", "destination queue region")
  clients := flag.Int("clients", 1, "number of clients")
  flag.Parse()

  if *src == "" || *dest == "" || *clients < 1 {
    flag.Usage()
    os.Exit(1)
  }

  log.Printf("source queue : %v", *src)
  log.Printf("destination queue : %v", *dest)
  log.Printf("number of clients : %v", *clients)

  sourceSession, err := session.NewSessionWithOptions(session.Options{
    SharedConfigState: session.SharedConfigEnable,
    Config: aws.Config{Region: aws.String(*srcRegion)},
  })
  destSession, _ := session.NewSessionWithOptions(session.Options{
    SharedConfigState: session.SharedConfigEnable,
    Config: aws.Config{Region: aws.String(*destRegion)},
  })
  if err != nil {
    panic(err)
  }

  maxMessages := int64(10)
  waitTime := int64(0)
  messageAttributeNames := aws.StringSlice([]string{"All"})
  attributeNames := aws.StringSlice([]string{"MessageDeduplicationId", "MessageGroupId"})

  rmin := &sqs.ReceiveMessageInput{
    QueueUrl:              src,
    MaxNumberOfMessages:   &maxMessages,
    WaitTimeSeconds:       &waitTime,
    MessageAttributeNames: messageAttributeNames,
    AttributeNames: attributeNames,
  }

  var wg sync.WaitGroup
  for i := 1; i <= *clients; i++ {
    wg.Add(i)
    go transferMessages(sourceSession, destSession, rmin, dest, &wg)
  }
  wg.Wait()

  log.Println("all done")
}

//transferMessages loops, transferring a number of messages from the src to the dest at an interval.
func transferMessages(sourceSession, destSession *session.Session, rmin *sqs.ReceiveMessageInput, dest *string, wgOuter *sync.WaitGroup) {
  sourceClient := sqs.New(sourceSession)
  destClient := sqs.New(destSession)

  lastMessageCount := 1

  defer wgOuter.Done()

  // loop as long as there are messages on the queue
  for {
    resp, err := sourceClient.ReceiveMessage(rmin)

    if err != nil {
      panic(err)
    }

    if lastMessageCount == 0 && len(resp.Messages) == 0 {
      // no messages returned twice now, the queue is probably empty
      log.Printf("done")
      return
    }

    lastMessageCount = len(resp.Messages)
    log.Printf("received %v messages...", len(resp.Messages))

    var wg sync.WaitGroup
    wg.Add(len(resp.Messages))

    for _, m := range resp.Messages {
      go func(m *sqs.Message) {
        defer wg.Done()

        // write the message to the destination queue
        smi := sqs.SendMessageInput{
          MessageAttributes: m.MessageAttributes,
          MessageBody:       m.Body,
          QueueUrl:          dest,
          MessageDeduplicationId: m.Attributes["MessageDeduplicationId"],
          MessageGroupId: m.Attributes["MessageGroupId"],
        }

        _, err := destClient.SendMessage(&smi)

        if err != nil {
          log.Printf("ERROR sending message to destination %v", err)
          return
        }

        // message was sent, dequeue from source queue
        dmi := &sqs.DeleteMessageInput{
         QueueUrl:      rmin.QueueUrl,
         ReceiptHandle: m.ReceiptHandle,
        }

        if _, err := sourceClient.DeleteMessage(dmi); err != nil {
         log.Printf("ERROR dequeueing message ID %v : %v",
           *m.ReceiptHandle,
           err)
        }
      }(m)
    }

    // wait for all jobs from this batch...
    wg.Wait()
  }
}
