# RedMQ
使用redis stream实现
消费者消费消息 ： 	

      1消费新消息
	
      2消费未确认的老消息
	
      3投递失败次数超过限制的消息进入死信队列
![image](https://github.com/pule1234/RedMQ/assets/112395669/0e247f15-f145-4f98-b5b6-5526279ef6bb)

生产消息： 通过XADD指令向topic中投入一组kv对消息

XADD topic1 * key2 val2

"1638515672769-0"

![image](https://github.com/pule1234/RedMQ/assets/112395669/fcd8e91b-1f7f-444b-83e9-45957e47c482)
![image](https://github.com/pule1234/RedMQ/assets/112395669/280ab6d0-e779-400f-98f9-ed13400572da)


用例在example中  其中死信队列和callback回调函数需要使用者自己定义


