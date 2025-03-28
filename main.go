package main

import (
	"fmt"
	"github.com/google/uuid"
	"log"
	"sync"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/blocto/solana-go-sdk/types"
	"github.com/mr-tron/base58/base58"
)

type TokenKey struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	PrivateKey string    `gorm:"unique;column:private_key"`
	PublicKey  string    `gorm:"unique;column:public_key"`
	IsPicked   bool      `gorm:"column:is_picked;default:false"`
	CreatedAt  time.Time `gorm:"column:created_at;default:now()"`
}

func (TokenKey) TableName() string {
	return "token_key"
}

func connectDB() *gorm.DB {
	dsn := "postgresql://postgres:123456@127.0.0.1:5432/postgres?sslmode=disable"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	return db
}

func generateVanityKeypair(suffix string) (string, string) {
	var mu sync.Mutex
	var account types.Account
	found := make(chan struct{}, 1)
	numWorkers := 4

	for i := 0; i < numWorkers; i++ {
		go func() {
			for {
				select {
				case <-found:
					return
				default:
					newAccount := types.NewAccount()
					pubkeyString := base58.Encode(newAccount.PublicKey[:])
					if len(pubkeyString) >= len(suffix) && pubkeyString[len(pubkeyString)-len(suffix):] == suffix {
						mu.Lock()
						account = newAccount
						mu.Unlock()
						select {
						case found <- struct{}{}:
						default:
						}
						return
					}
				}
			}
		}()
	}

	<-found
	privateKey := base58.Encode(account.PrivateKey[:])
	publicKey := base58.Encode(account.PublicKey[:])
	return privateKey, publicKey
}

func checkAndGenerateKeys(db *gorm.DB) {
	for {
		var count int64
		db.Model(&TokenKey{}).Count(&count)

		fmt.Println("Current number of keys:", count)
		if count < 50 {
			privateKey, publicKey := generateVanityKeypair("ponz")

			newKey := TokenKey{
				ID:         uuid.NewString(),
				PrivateKey: privateKey,
				PublicKey:  publicKey,
				IsPicked:   false,
			}

			if err := db.Create(&newKey).Error; err != nil {
				log.Println("Error inserting new key:", err)
			} else {
				fmt.Println("Added new key:", publicKey)
			}
			continue
		}

		time.Sleep(time.Minute)
	}
}

func main() {
	db := connectDB()
	go checkAndGenerateKeys(db)
	select {}
}
