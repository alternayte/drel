package main

import (
	"context"
	"fmt"
	"log"

	"github.com/alternayte/drel/examples/value-objects/accounts"
	"github.com/alternayte/drel/examples/value-objects/db"
)

func main() {
	ctx := context.Background()
	database, err := db.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	if _, err := database.Exec(ctx, `CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner TEXT NOT NULL,
		balance_amount TEXT NOT NULL,
		balance_currency TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		log.Fatal(err)
	}

	if err := database.WithTx(ctx, func(ctx context.Context) error {
		database.Tx(ctx).Accounts.Add(accounts.NewAccount("alice", accounts.NewMoney(100, "USD")))
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	loaded, err := database.Accounts.First(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("account %s balance %d %s\n", loaded.Owner(), loaded.Balance().Amount(), loaded.Balance().Currency())
}
