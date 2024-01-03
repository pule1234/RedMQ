package RedMQ

import (
	"RedMQ/redis"
	"context"
)

type Producer struct {
	// 内置的redis客户端
	client *redis.Client
	// 用户定义的生产者配置参数
	opts *ProducerOptions
}

// 生产者producer的构造器函数
func NewProducer(client *redis.Client, opts ...ProducerOption) *Producer {
	p := Producer{
		client: client,
		opts:   &ProducerOptions{},
	}

	for _, opt := range opts {
		opt(p.opts)
	}

	// 数据修复
	repairProducer(p.opts)
	return &p
}

// 生产一条消息
func (p *Producer) SendMsg(ctx context.Context, topic, key, val string) (string, error) {
	return p.client.XADD(ctx, topic, p.opts.msgQueueLen, key, val)
}
