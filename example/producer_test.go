package example

import (
	"RedMQ"
	"RedMQ/redis"
	"context"
	"testing"
)

func Tets_Producer(t *testing.T) {
	client := redis.NewClient(network, address, password)
	// 最多保留10条消息

	producer := RedMQ.NewProducer(client, RedMQ.WithMsgQueueLen(10))
	ctx := context.Background()

	msgID, err := producer.SendMsg(ctx, topic, "test_kk", "test_vv")
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(msgID)
}
