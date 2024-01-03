package redis

import (
	"context"
	"testing"
)

const (
	network  = "tcp"
	address  = "localhost:6379"
	password = ""
)

func Test_redis_xadd(t *testing.T) {
	client := NewClient(network, address, password)
	ctx := context.Background()
	res, err := client.XADD(ctx, "test_stream_topic", 3, "test_key", "test_val")
	if err != nil {
		t.Error(err)
		return
	}

	t.Log(res)
}

func Test_redis_xreadergroup(t *testing.T) {
	client := NewClient(network, address, password)
	ctx := context.Background()
	res, err := client.XReadGroupPending(ctx, "mygroup_4", "my_consumer", "stream_topic")
	if err != nil {
		t.Error(err)
		return
	}
	t.Log(res)
}
