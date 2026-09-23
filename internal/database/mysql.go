package database

import (
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func OpenMySQL(dsn, migrationsDir string) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	if err := goose.SetDialect("mysql"); err != nil {
		return nil, fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return db, nil
}
