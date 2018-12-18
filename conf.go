package sentinelstore


type RedisConf struct {
	Network string
	Password string
	DB int
	TimeOut int
	Pool int
	MasterName string
	Sentinels []string
}

