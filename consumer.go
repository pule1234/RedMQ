package RedMQ

import (
	"context"
	"errors"
	"github.com/pule1234/RedMQ/log"
	"github.com/pule1234/RedMQ/redis"
)

// 接收到消息后执行的回调函数
type MsgCallBack func(ctx context.Context, msg *redis.MsgEntity) error

// 消费者
type Consumer struct {
	// consumer 生命周期管理
	ctx  context.Context
	stop context.CancelFunc

	// 接收到msg时执行的回调函数,由使用方定义
	callbackFunc MsgCallBack

	//redis客户端，基于redis实现message queue
	client *redis.Client

	// 消费的topic
	topic string
	// 所属的消费者组
	groupID string
	// 当前节点的消费者id
	consumerID string
	// 各消息累计失败次数
	failureCnts map[redis.MsgEntity]int
	//一些用户自定义的配置
	opts *ConsumerOptions
}

func NewCusumer(client *redis.Client, topic, groupID, consumerID string, callbackfunc MsgCallBack, opts ...ConsumerOption) (*Consumer, error) {
	// cancel context，用于提供停止 consumer 的控制器
	ctx, stop := context.WithCancel(context.Background())
	c := Consumer{
		client:       client,
		ctx:          ctx,
		stop:         stop,
		callbackFunc: callbackfunc,
		topic:        topic,
		groupID:      groupID,
		consumerID:   consumerID,
		opts:         &ConsumerOptions{},
		failureCnts:  make(map[redis.MsgEntity]int),
	}

	// 校验参数
	if err := c.checkParam(); err != nil {
		return nil, err
	}

	for _, opt := range opts {
		opt(c.opts)
	}

	repairConsumer(c.opts)

	// 启动consumer 守护 goroutine ， 负责轮询消费消息
	go c.run()
	// 返回consumer实例
	return &c, nil
}

func (c *Consumer) checkParam() error {
	if c.callbackFunc == nil {
		return errors.New("callback function cant be empty")
	}

	if c.client == nil {
		return errors.New("redis client cant be empty")
	}

	if c.topic == "" || c.consumerID == "" || c.groupID == "" {
		return errors.New("topic | groupid | consumer_id cant be empty")
	}

	return nil
}

func (c *Consumer) Stop() {
	c.stop()
}

// 运行消费者
func (c *Consumer) run() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		// 新消息被接受处理
		msgs, err := c.receive()
		if err != nil {
			log.ErrorContextf(c.ctx, "receive msg failed, err: %v", err)
			continue
		}

		// 接收到新消息之后调用callback回调方法
		tctx, _ := context.WithTimeout(c.ctx, c.opts.handleMsgsTimeout)
		c.handlerMsgs(tctx, msgs)

		//把失败次数超限的老消息投递到死信队列
		tctx, _ = context.WithTimeout(c.ctx, c.opts.deadLetterDeliverTimeout)
		c.deliverDeadLetter(tctx)

		// 接收之前就分配到给当前consumer，但是还未得到xack确认的老消息
		pendingMsgs, err := c.receivePending()
		if err != nil {
			log.ErrorContextf(c.ctx, "pending msg received failed , err : %v", err)
			continue
		}
		//接收到老消息后，执行对应的callback回调方法
		tctx, _ = context.WithTimeout(c.ctx, c.opts.handleMsgsTimeout)
		c.handlerMsgs(tctx, pendingMsgs)
	}

}

func (c *Consumer) receive() ([]*redis.MsgEntity, error) {
	msgs, err := c.client.XReadGroup(c.ctx, c.groupID, c.consumerID, c.topic, int(c.opts.receiveTimeout.Milliseconds()))
	if err != nil && !errors.Is(err, redis.ErrNoMsg) {
		return nil, err
	}
	return msgs, nil
}

func (c *Consumer) receivePending() ([]*redis.MsgEntity, error) {
	pendingMsgs, err := c.client.XReadGroupPending(c.ctx, c.groupID, c.consumerID, c.topic)
	if err != nil && !errors.Is(err, redis.ErrNoMsg) {
		return nil, err
	}
	return pendingMsgs, nil
}

func (c *Consumer) handlerMsgs(ctx context.Context, msgs []*redis.MsgEntity) {
	for _, msg := range msgs {
		if err := c.callbackFunc(ctx, msg); err != nil {
			// 失败计数器累加
			c.failureCnts[*msg]++
			continue
		}

		// callback执行成功， 进行ack
		if err := c.client.XACK(ctx, c.topic, c.groupID, msg.MsgID); err != nil {
			// ack 失败的情况需要关注
			log.ErrorContextf(ctx, "msg ack failed, msg id: %s, err: %v", msg.MsgID, err)
			continue
		}

		// ack 成功了，从 map中清零对应消息的失败次数
		delete(c.failureCnts, *msg)
	}
}

func (c *Consumer) deliverDeadLetter(ctx context.Context) {
	// 对于失败达到指定次数的消息，投递到死信中，然后执行ack
	for msg, failureCnt := range c.failureCnts {
		if failureCnt < c.opts.maxRetryLimit {
			continue
		}

		// 投递死信队列
		if err := c.opts.deadLetterMailbox.Deliver(ctx, &msg); err != nil {
			log.ErrorContextf(c.ctx, "dead letter deliver failed, msg id: %s, err: %v", msg.MsgID, err)
			continue
		}

		// 执行ack响应
		if err := c.client.XACK(ctx, c.topic, c.groupID, msg.MsgID); err != nil {
			log.ErrorContextf(c.ctx, "msg ack failed, msg id: %s, err: %v", msg.MsgID, err)
			continue
		}

		// 对于 ack 成功的消息，将其从 failure map 中删除
		delete(c.failureCnts, msg)
	}
}
