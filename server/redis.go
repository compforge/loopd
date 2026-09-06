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
	ReadBlock    time.Duration // Defaults to one second.
	ReadCount    int64         // Defaults to 100 events per read.
	KeyPrefix    string        // Defaults to loopd:agentue.
}
