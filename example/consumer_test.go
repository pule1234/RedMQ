package example

import (
	"RedMQ"
	"RedMQ/redis"
	"context"
	"testing"
	"time"
)

const (
	network       = "tcp"
	address       = "localhost:6379"
	password      = ""
	topic         = ""
	consumerGroup = "请输入消费者组名称"
	consumerID    = "请输入消费者名称"
)

// 自定义实现的死信队列
type DemoDeadLetterMailbox struct {
	do func(msg *redis.MsgEntity)
}

func NewDemoDeadLetterMailbox(do func(msg *redis.MsgEntity)) *DemoDeadLetterMailbox {
	return &DemoDeadLetterMailbox{
		do: do,
	}
}

// 死信队列接收消息的处理方法
func (d *DemoDeadLetterMailbox) Deliver(ctx context.Context, msg *redis.MsgEntity) error {
	d.do(msg)
	return nil
}

func Test_Consumer(t *testing.T) {
	// 创建redis客户端
	client := redis.NewClient(network, address, password)

	// consumer 接收到消息后执行的callback回调处理函数
	callbackFunc := func(ctx context.Context, msg *redis.MsgEntity) error {
		t.Logf("receive msg, msg id: %s, msg key: %s, msg val: %s", msg.MsgID, msg.Key, msg.Value)
		return nil
	}

	// 创建自定义实现的死信队列实例
	demoDeadLetterMailbox := NewDemoDeadLetterMailbox(func(msg *redis.MsgEntity) {
		t.Logf("receive dead letter, msg id: %s, msg key: %s, msg val: %s", msg.MsgID, msg.Key, msg.Value)
	})

	// 构造并启动消费者consumer实例
	consumer, err := RedMQ.NewCusumer(client, topic, consumerGroup, consumerID, callbackFunc,
		//每条消息最多重试处理2次
		RedMQ.WithMaxRetryLimit(2),
		// 每轮接收消息的阻塞等待超时时间为2s
		RedMQ.WithReceiveTimeout(2*time.Second),
		// 注入自定义实现的死信队列
		RedMQ.WithDeadLetterMailbox(demoDeadLetterMailbox), )

	if err != nil {
		t.Error(err)
		return
	}
	// 程序推出前停止consumer
	defer consumer.Stop()

	// 10秒后退出单测程序
	<-time.After(10 * time.Second)
}
