package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blocto/solana-go-sdk/types"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/mr-tron/base58/base58"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TokenKey struct {
	ID         string    `gorm:"type:uuid;primaryKey"`
	PrivateKey string    `gorm:"unique;column:private_key"`
	PublicKey  string    `gorm:"unique;column:public_key"`
	IsPicked   bool      `gorm:"column:is_picked;default:false"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (TokenKey) TableName() string { return "token_key" }

func connectDB(dsn string) *gorm.DB {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	return db
}

type Keypair struct {
	Priv string
	Pub  string
}

func generateVanityKeypair(ctx context.Context, suffix string, workers int) (Keypair, error) {
	suffix = strings.TrimSpace(suffix)
	found := make(chan Keypair, 1)

	worker := func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				acc := types.NewAccount()
				pub := base58.Encode(acc.PublicKey[:])
				if strings.HasSuffix(pub, suffix) {
					found <- Keypair{
						Priv: base58.Encode(acc.PrivateKey[:]),
						Pub:  pub,
					}
					return
				}
			}
		}
	}

	for i := 0; i < workers; i++ {
		go worker()
	}

	select {
	case <-ctx.Done():
		return Keypair{}, ctx.Err()
	case kp := <-found:
		return kp, nil
	}
}

func countUnpicked(db *gorm.DB) (int64, error) {
	var c int64
	err := db.Model(&TokenKey{}).Where("is_picked = false").Count(&c).Error
	return c, err
}

func maintainUnpickedKeys(db *gorm.DB, target int, suffix string, sleepDur time.Duration, workers int) {
	for {
		c, err := countUnpicked(db)
		if err != nil {
			log.Println("Error counting unpicked keys:", err)
			time.Sleep(10 * time.Second)
			continue
		}

		if c >= int64(target) {
			log.Printf("Enough unpicked keys (%d >= %d). Sleeping for %v...\n", c, target, sleepDur)
			time.Sleep(sleepDur)
			continue
		}

		log.Printf("Unpicked keys below target: %d / %d. Generating...\n", c, target)
		for c < int64(target) {
			ctx, cancel := context.WithCancel(context.Background())
			kp, err := generateVanityKeypair(ctx, suffix, workers)
			cancel()
			if err != nil {
				log.Println("Error generating vanity key:", err)
				time.Sleep(1 * time.Second)
				continue
			}

			newKey := TokenKey{
				ID:         uuid.NewString(), // Generate UUID in code
				PrivateKey: kp.Priv,
				PublicKey:  kp.Pub,
				IsPicked:   false,
			}

			err = db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "public_key"}},
				DoNothing: true,
			}).Create(&newKey).Error
			if err != nil {
				log.Println("Error inserting key:", err)
				continue
			}

			c, err = countUnpicked(db)
			if err != nil {
				log.Println("Error recounting unpicked keys:", err)
				break
			}
			log.Printf("Added key: %s | Current unpicked: %d\n", newKey.PublicKey, c)
		}

		log.Printf("Target %d unpicked reached. Sleeping for %v...\n", target, sleepDur)
		time.Sleep(sleepDur)
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file:", err)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}
	db := connectDB(dsn)

	targetUnpicked := 100
	if val := os.Getenv("TARGET_UNPICKED"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			targetUnpicked = v
		}
	}

	suffix := "ponz"
	if val := os.Getenv("SUFFIX"); val != "" {
		suffix = val
	}

	sleepMinutes := 1
	if val := os.Getenv("SLEEP_MINUTES"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			sleepMinutes = v
		}
	}

	workers := 100
	if val := os.Getenv("WORKERS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			workers = v
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Keep at least targetUnpicked unpicked keys, sleep sleepMinutes when enough
	go maintainUnpickedKeys(db, targetUnpicked, suffix, time.Duration(sleepMinutes)*time.Minute, workers)

	<-ctx.Done()
	fmt.Println("Shutting down...")
}
