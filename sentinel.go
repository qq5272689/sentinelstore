package sentinelstore


import (
	"time"
	radix "github.com/qq5272689/radix"
	"fmt"
	"errors"
)


func sentinelConnFunc(network,addr string) (radix.Conn,error)  {
	c,err:=radix.Dial(network,addr,radix.DialTimeout(time.Millisecond * time.Duration(MyRedisConf.TimeOut)))
	if err!=nil{
		Logger.Errorln("sentinel conn dial 报错！！！err:",err)
		return c,err
	}
	var ping_result string
	err=c.Do(radix.Cmd(&ping_result,"ping"))
	if err!=nil{
		Logger.Errorln("sentinel conn do ping 报错！！！err:",err)
		return c,err
	}
	if ping_result!="PONG"{
		Logger.Errorln("sentinel conn do ping 没有受到PONG！！！ping_result:",ping_result)
		return c,errors.New(fmt.Sprintln("sentinel conn do ping 没有受到PONG！！！ping_result:",ping_result))
	}
	Logger.Debugln("连接sentine 成功！！！")
	return c,nil
}

// redis 连接函数
func redisConnFunc(network,addr string) (radix.Conn, error)  {
	c,err:=radix.Dial(network,addr,radix.DialAuthPass(MyRedisConf.Password),radix.DialSelectDB(MyRedisConf.DB),
		radix.DialTimeout(time.Millisecond * time.Duration(MyRedisConf.TimeOut)))
	if err!=nil{
		Logger.Logger.Errorln("redis conn dial 报错！！！err:",err)
		return c,err
	}
	var ping_result string
	err=c.Do(radix.Cmd(&ping_result,"ping"))
	if err!=nil{
		Logger.Logger.Errorln("redis conn do ping 报错！！！err:",err)
		return c,err
	}
	if ping_result!="PONG"{
		Logger.Logger.Errorln("redis conn do ping 没有受到PONG！！！ping_result:",ping_result)
		return c,errors.New(fmt.Sprintln("redis conn do ping 没有受到PONG！！！ping_result:",ping_result))
	}
	Logger.Logger.Debugln("连接 redis 成功！！！")
	return c,nil
}

func redisPoolFunc(network,addr string) (radix.Client,error)  {

	p,err:=radix.NewPool(network,addr,MyRedisConf.Pool,radix.PoolConnFunc(redisConnFunc),radix.PoolOnFullClose())
	if err!=nil{
		Logger.Logger.Errorln("redis pool dial 报错！！！errr:",err)
		return p,err
	}
	return p,nil
}


var Sentinel *radix.Sentinel
var MyRedisConf *RedisConf


func RedisInit(c *RedisConf) (*radix.Sentinel,error) {
	MyRedisConf=c
	scf:=radix.SentinelConnFunc(sentinelConnFunc)
	spf:=radix.SentinelPoolFunc(redisPoolFunc)
	sentinel,err:= radix.NewSentinel(MyRedisConf.MasterName,MyRedisConf.Sentinels,scf,spf)
	if err!=nil{
		Logger.Errorln("创建 new sentinel 报错！！！errr:",err)
		return nil,err
	}
	Sentinel=sentinel
	Logger.Infoln("Redis 初始化成功！")
	return sentinel,nil
}