package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"im/internal/api"
	"im/internal/config"
	"im/internal/dao"
	"im/internal/model"
	"im/internal/persist"
	"im/internal/presence"
	"im/internal/service"
	"im/internal/transport"
	"im/internal/transport/kafka"
	"im/internal/transport/local"
	"im/internal/ws"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	log.SetPrefix("[" + cfg.InstanceID + "] ")
	log.Printf("starting instance=%s addr=%s mode=%s", cfg.InstanceID, cfg.HTTPAddr, cfg.Transport.Mode)
	gin.SetMode(gin.ReleaseMode)

	db, err := gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	// 两个实例同时启动时会并发建表，后到的那个会报 "already exists"；重试一次就能看到已建好的表
	for attempt := 1; ; attempt++ {
		err := db.AutoMigrate(&model.User{}, &model.Group{}, &model.GroupMember{}, &model.Message{})
		if err == nil {
			break
		}
		if attempt == 3 {
			log.Fatalf("migrate: %v", err)
		}
		log.Printf("migrate attempt %d: %v (retrying)", attempt, err)
		time.Sleep(time.Second)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(50)
		sqlDB.SetMaxIdleConns(25)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err != nil {
			log.Fatalf("redis ping: %v", err)
		}
	}

	userDAO := dao.NewUserDAO(db)
	groupDAO := dao.NewGroupDAO(db)
	msgDAO := dao.NewMessageDAO(db)
	pres := presence.New(rdb, cfg.InstanceID)

	userSvc := service.NewUserService(userDAO, cfg.JWTSecret)
	groupSvc := service.NewGroupService(groupDAO, userDAO)
	convSvc := service.NewConvService(groupDAO)
	hub := ws.NewHub(groupSvc, pres)
	groupSvc.SetInvalidator(hub)

	var t transport.Transport
	switch cfg.Transport.Mode {
	case "local":
		maxSeq, err := msgDAO.MaxSeq()
		if err != nil {
			log.Fatalf("load max seq: %v", err)
		}
		log.Printf("local transport: seq continues from %d", maxSeq)
		t = local.New(cfg.Transport.Local, maxSeq)
	case "kafka":
		t = kafka.New(cfg.Transport.Kafka, cfg.InstanceID)
	default:
		log.Fatalf("unknown transport mode %q", cfg.Transport.Mode)
	}

	persister := persist.New(msgDAO)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := t.Subscribe(ctx, transport.Subscription{Name: "deliver", Broadcast: true}, hub.Deliver); err != nil {
		log.Fatalf("subscribe deliver: %v", err)
	}
	if err := t.Subscribe(ctx, transport.Subscription{Name: "persist", Broadcast: false}, persister.Enqueue); err != nil {
		log.Fatalf("subscribe persist: %v", err)
	}
	if err := t.Start(ctx); err != nil {
		log.Fatalf("start transport: %v", err)
	}

	msgSvc := service.NewMessageService(msgDAO, convSvc, t, pres)
	fileSvc, err := service.NewFileService(cfg.UploadDir)
	if err != nil {
		log.Fatalf("upload dir: %v", err)
	}
	presSvc := service.NewPresenceService(pres)

	router := api.NewRouter(api.Deps{
		BaseCtx:   ctx,
		UploadDir: cfg.UploadDir,
		Users:     userSvc,
		Groups:    groupSvc,
		Messages:  msgSvc,
		Files:     fileSvc,
		Presence:  presSvc,
		Hub:       hub,
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()
	log.Printf("listening on %s", cfg.HTTPAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")

	// 顺序很重要：先停入口（HTTP + WS），再关 Transport（dispatcher 处理完已接收的消息），
	// 最后关 persister（把这些消息写完）。反过来会丢已经给发送者回显过的消息。
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	hub.CloseAll()
	if err := t.Close(); err != nil {
		log.Printf("transport close: %v", err)
	}
	persister.Close()
	cancel()
	if err := rdb.Close(); err != nil {
		log.Printf("redis close: %v", err)
	}
	log.Printf("bye")
}
