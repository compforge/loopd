package server

import "time"

type RedisConfig struct {
	Address      string
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
	MinIdleConns int
	TaskTTL      time.Duration // Defaults to 30 days and is refreshed when events arrive.
	ReadBlock    time.Duration // Defaults to one second.
	ReadCount    int64         // Defaults to 100 events per read.
	KeyPrefix    string        // Defaults to loopd:agentue.
}
