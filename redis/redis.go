package redis

import (
	"context"
	"errors"
	"fmt"
	"github.com/demdxx/gocast"
	"github.com/gomodule/redigo/redis"
	"strings"
	"time"
)

type MsgEntity struct {
	MsgID string // 消息的id
	Key   string
	Value string
}

var ErrNoMsg = errors.New("no msg received")

// client redis 客户端
type Client struct {
	opts *ClientOptions // 用户自定义配置
	pool *redis.Pool    // redis连接池
}

// 其中网络协议，redis地址，redis密码为必填项
func NewClient(network, address, password string, opts ...ClientOption) *Client {
	c := Client{
		opts: &ClientOptions{
			network:  network,
			address:  address,
			password: password,
		},
	}

	// 注入用户自定义的配置参数
	for _, opt := range opts {
		opt(c.opts)
	}

	// 对非法的配置参数进行修复
	repairClient(c.opts)

	//创建redis连接池
	pool := c.getRedisPool()
	// 返回redis客户端实例
	return &Client{
		pool: pool,
	}
}

func NewClientWithPool(pool *redis.Pool, opts ...ClientOption) *Client {
	c := Client{
		pool: pool,
		opts: &ClientOptions{},
	}

	for _, opt := range opts {
		opt(c.opts)
	}

	repairClient(c.opts)

	return &c
}

// 获取redis连接池
func (c *Client) getRedisPool() *redis.Pool {
	return &redis.Pool{
		// 最大空闲连接数
		MaxIdle: c.opts.maxIdle,
		// 连接最长空闲时间
		IdleTimeout: time.Duration(c.opts.idleTimeoutSeconds) * time.Second,
		// 创建连接的方法
		Dial: func() (redis.Conn, error) {
			c, err := c.getRedisConn()
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		// 最大活跃连接数
		MaxActive: c.opts.maxActive,
		// 当链接不够时，是阻塞等待还是立即返回
		Wait: c.opts.wait,
		// 测试方法
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("Ping")
			return err
		},
	}
}

func (c *Client) GetConn(ctx context.Context) (redis.Conn, error) {
	return c.pool.GetContext(ctx)
}

// 获取redis配置
func (c *Client) getRedisConn() (redis.Conn, error) {
	if c.opts.address == "" {
		panic("cannot get redis address from config")
	}

	// 注入密码
	var dialOpts []redis.DialOption
	if len(c.opts.password) > 0 {
		dialOpts = append(dialOpts, redis.DialPassword(c.opts.password))
	}

	// 创建新的连接
	return redis.DialContext(context.Background(), c.opts.network, c.opts.address, dialOpts...)
}

// 投递信息
func (c *Client) XADD(ctx context.Context, topic string, maxLen int, key, val string) (string, error) {
	// topic 名称不能为空
	if topic == "" {
		return "", errors.New("redis XADD topic can't be empty")
	}
	// 从redis连接池中获取连接
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return "", err
	}
	// 使用完毕后把连接放回到连接池
	defer conn.Close()
	// 执行XADD指令，并返回生成的消息id
	return redis.String(conn.Do("XADD", topic, "MAXLEN", maxLen, "*", key, val))
}

// 确认消息
func (c *Client) XACK(ctx context.Context, topic, groupID, msgID string) error {
	if topic == "" || groupID == "" || msgID == "" {
		return errors.New("redis XACK topic | group_id | msg_ id can't be empty")
	}
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 执行确认命令
	reply, err := redis.Int64(conn.Do("XACK", topic, groupID, msgID))
	if err != nil {
		return err
	}
	if reply != 1 {
		return fmt.Errorf("invalid reply : %d", reply)
	}

	return nil
}

// 消费消息时使用的是 redis streams 的 XREADGROUP 指令，
// 其中又可以分为消费新消息（尚未分配给任何 consumer 的消息）以及消费未确认的老消息(未ack的消息)
// 消费新消息
func (c *Client) XReadGroup(ctx context.Context, groupID, consumerID, topic string, timeoutMiliSeconds int) ([]*MsgEntity, error) {
	return c.xReadGroup(ctx, groupID, consumerID, topic, timeoutMiliSeconds, false)
}

// 重复消费未确认消息  ，   调用xReadGroup   将pending标识置为true
func (c *Client) XReadGroupPending(ctx context.Context, groupID, consumerID, topic string) ([]*MsgEntity, error) {
	return c.xReadGroup(ctx, groupID, consumerID, topic, 0, true)
}

// 根据用户传入的pending表示是否为true，代表当前时消费处理新消息还是处理未确认的老消息
func (c *Client) xReadGroup(ctx context.Context, groupID, consumerID, topic string, timeoutMilSeconds int, pending bool) ([]*MsgEntity, error) {
	//消费者组id，消费者id，topic缺一不可
	if groupID == "" || consumerID == "" || topic == "" {
		return nil, errors.New("redis XREADGROUP groupID/consumerID/topic can't be empty")
	}
	// 从redis连接池中获取新的连接
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var rawReply interface{}
	// 倘若pending为true ， 代表需要消费的是已分配给当前consumer但是还未经xack确认的消息， 此时采用非阻塞模式进行处理
	if pending {
		rawReply, err = conn.Do("XREADGROUP", "GROUP", groupID, consumerID, "STREAMS", topic, "0-0")
	} else {
		// 倘若 pending 为 false，代表需要消费的是尚未分配给任何 consumer 的新消息，此时会才用阻塞模式执行操作
		rawReply, err = conn.Do("XREADGROUP", "GROUP", groupID, consumerID, "BLOCK", timeoutMilSeconds, "STREAMS", topic, ">")
	}

	if err != nil {
		return nil, err
	}
	reply, _ := rawReply.([]interface{})
	if len(reply) == 0 {
		return nil, ErrNoMsg
	}

	replyElement, _ := reply[0].([]interface{})
	if len(replyElement) != 2 {
		return nil, errors.New("invalid msg format")
	}

	//对消费到的数据进行格式化
	var msgs []*MsgEntity
	rawMsgs, _ := replyElement[1].([]interface{})
	for _, rawMsg := range rawMsgs {
		_msg, _ := rawMsg.([]interface{})
		if len(_msg) != 2 {
			return nil, errors.New("invalid msg format")
		}
		msgID := gocast.ToString(_msg[0])
		msgBody, _ := _msg[1].([]interface{})
		if len(msgBody) != 2 {
			return nil, errors.New("invalid msg format")
		}
		msgKey := gocast.ToString(msgBody[0])
		msgVal := gocast.ToString(msgBody[1])
		msgs = append(msgs, &MsgEntity{
			MsgID: msgID,
			Key:   msgKey,
			Value: msgVal,
		})
	}

	return msgs, nil
}

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", errors.New("redis GET key can't be empty")
	}

	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	return redis.String(conn.Do("GET", key))
}

func (c *Client) Set(ctx context.Context, key, val string) (int64, error) {
	if key == "" || val == "" {
		return -1, errors.New("redis SET key or value can't be empty")
	}
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return -1, err
	}

	defer conn.Close()
	resp, err := conn.Do("SET", key, val)
	if err != nil {
		return -1, err
	}

	if respStr, ok := resp.(string); ok && strings.ToLower(respStr) == "ok" {
		return 1, nil
	}
	return redis.Int64(resp, err)
}

func (c *Client) SetNEX(ctx context.Context, key, value string, expireSeconds int64) (int64, error) {
	if key == "" || value == "" {
		return -1, errors.New("redis SET keyNX or value can't be empty")
	}

	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return -1, err
	}
	defer conn.Close()

	reply, err := conn.Do("SET", key, value, "EX", expireSeconds, "NX")
	if err != nil {
		return -1, err
	}

	if respStr, ok := reply.(string); ok && strings.ToLower(respStr) == "ok" {
		return 1, nil
	}

	return redis.Int64(reply, err)
}

func (c *Client) SetNX(ctx context.Context, key, value string) (int64, error) {
	if key == "" || value == "" {
		return -1, errors.New("redis SET key NX or value can't be empty")
	}

	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return -1, err
	}
	defer conn.Close()

	reply, err := conn.Do("SET", key, value, "NX")
	if err != nil {
		return -1, err
	}

	if respStr, ok := reply.(string); ok && strings.ToLower(respStr) == "ok" {
		return 1, nil
	}

	return redis.Int64(reply, err)
}

func (c *Client) Del(ctx context.Context, key string) error {
	if key == "" {
		return errors.New("redis DEL key can't be empty")
	}

	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return err
	}

	defer conn.Close()
	_, err = conn.Do("DEL", key)
	return err
}

func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	if key == "" {
		return -1, errors.New("redis INCR key can't be empty")
	}

	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return -1, err
	}
	defer conn.Close()

	return redis.Int64(conn.Do("INCR", key))
}

// // Eval 支持使用 lua 脚本.
// /src（Lua 脚本字符串）、keyCount（键的数量）、keysAndArgs（包含键和参数的切片
func (c *Client) Eval(ctx context.Context, src string, keyCount int, keysAndArgs []interface{}) (interface{}, error) {
	args := make([]interface{}, 2+len(keysAndArgs))
	args[0] = src
	args[1] = keyCount
	copy(args[2:], keysAndArgs)

	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return -1, err
	}
	defer conn.Close()

	return conn.Do("EVAL", args)
}

// 在topic上创建新的消费者组
func (c *Client) XGroupCreate(ctx context.Context, topic, group string) (string, error) {
	conn, err := c.pool.GetContext(ctx)
	if err != nil {
		return "", nil
	}

	defer conn.Close()
	return redis.String(conn.Do("XGROUP", "CREATE", topic, group, "0-0"))
}
