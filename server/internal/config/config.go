package config

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type LocalConfig struct {
	BufferSize       int `yaml:"buffer_size"`
	PublishTimeoutMS int `yaml:"publish_timeout_ms"`
}

type KafkaConfig struct {
	Brokers []string `yaml:"brokers"`
	Topic   string   `yaml:"topic"`
}

type TransportConfig struct {
	Mode  string      `yaml:"mode"`
	Local LocalConfig `yaml:"local"`
	Kafka KafkaConfig `yaml:"kafka"`
}

type Config struct {
	InstanceID string          `yaml:"instance_id"`
	HTTPAddr   string          `yaml:"http_addr"`
	MySQLDSN   string          `yaml:"mysql_dsn"`
	RedisAddr  string          `yaml:"redis_addr"`
	JWTSecret  string          `yaml:"jwt_secret"`
	UploadDir  string          `yaml:"upload_dir"`
	Transport  TransportConfig `yaml:"transport"`
}

// Load 读 yaml，再用命令行 flag 覆盖，这样同一份 config.yaml 能起多个实例。
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	path := fs.String("config", "config.yaml", "config file path")
	id := fs.String("id", "", "override instance_id")
	addr := fs.String("addr", "", "override http_addr")
	mode := fs.String("mode", "", "override transport.mode (local|kafka)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	b, err := os.ReadFile(*path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if *id != "" {
		c.InstanceID = *id
	}
	if *addr != "" {
		c.HTTPAddr = *addr
	}
	if *mode != "" {
		c.Transport.Mode = *mode
	}

	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
	}
	if c.UploadDir == "" {
		c.UploadDir = "./uploads"
	}
	if c.Transport.Mode == "" {
		c.Transport.Mode = "local"
	}
	if c.Transport.Local.BufferSize <= 0 {
		c.Transport.Local.BufferSize = 4096
	}
	if c.Transport.Local.PublishTimeoutMS <= 0 {
		c.Transport.Local.PublishTimeoutMS = 50
	}
	if c.Transport.Kafka.Topic == "" {
		c.Transport.Kafka.Topic = "im-messages"
	}
}

func (c *Config) validate() error {
	if c.InstanceID == "" {
		return errors.New("config: instance_id is required")
	}
	if c.JWTSecret == "" {
		return errors.New("config: jwt_secret is required")
	}
	if c.MySQLDSN == "" {
		return errors.New("config: mysql_dsn is required")
	}
	if c.RedisAddr == "" {
		return errors.New("config: redis_addr is required")
	}
	return nil
}
