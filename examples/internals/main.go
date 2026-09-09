// Example: internals
//
// Shows what drel's codegen produces under the hood. Everything here —
// ModelMeta, Scan, Snapshot, Diff, column references — is normally
// generated into <model>_drel.go files. This example hand-crafts them
// so you can see the machinery without running the code generator.
//
// Covers: queries (Find, Where, OrderBy, Limit, OR, Count, Exists,
// string Contains) AND transactions (insert, update with change
// tracking, delete, rollback, mid-tx SaveChanges).
//
// It shows both transaction forms. Engine.WithTx puts the transaction in the
// context, which is what generated code does through db.WithTx and db.Tx(ctx).
// Engine.Transaction passes the transaction by hand.
//
// Usage:
//
//	export DATABASE_URL="postgres://localhost:5432/drelexample?sslmode=disable"
//	go run ./examples/internals/
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/alternayte/drel"
)

// Product uses exported fields — no drel.Model[K] embed.
// Codegen works with any struct shape; this is the simplest.
type Product struct {
	ID        int
	Name      string
	Price     int
	InStock   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ProductMeta is what codegen emits as a package-level var.
// It tells Repository how to scan rows, track changes, and build inserts.
var ProductMeta = drel.ModelMeta[Product]{
	Table:    "products",
	Columns:  []string{"id", "name", "price", "in_stock", "created_at", "updated_at"},
	PKColumn: "id",

	Scan: func(row drel.Row) (*Product, error) {
		p := &Product{}
		return p, row.Scan(&p.ID, &p.Name, &p.Price, &p.InStock, &p.CreatedAt, &p.UpdatedAt)
	},

	// Snapshot captures mutable fields at load time.
	Snapshot: func(p *Product) any {
		return [3]any{p.Name, p.Price, p.InStock}
	},

	// Diff compares current state to the snapshot and returns only changed columns.
	Diff: func(p *Product, snap any) []drel.FieldChange {
		s := snap.([3]any)
		var changes []drel.FieldChange
		if p.Name != s[0].(string) {
			changes = append(changes, drel.FieldChange{Column: "name", Value: p.Name})
		}
		if p.Price != s[1].(int) {
			changes = append(changes, drel.FieldChange{Column: "price", Value: p.Price})
		}
		if p.InStock != s[2].(bool) {
			changes = append(changes, drel.FieldChange{Column: "in_stock", Value: p.InStock})
		}
		return changes
	},

	PKValue: func(p *Product) any { return p.ID },
	InsertColumns: func(p *Product) ([]string, []any) {
		return []string{"name", "price", "in_stock"}, []any{p.Name, p.Price, p.InStock}
	},
	ScanReturning: func(p *Product, row drel.Row) error { return row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt) },
}

// Products provides typed column references for the query builder.
var Products = struct {
	ID      drel.OrderedColumn[int]
	Name    drel.StringColumn
	Price   drel.OrderedColumn[int]
	InStock drel.BoolColumn
}{
	ID:      drel.NewOrderedCol[int]("id"),
	Name:    drel.NewStringCol("name"),
	Price:   drel.NewOrderedCol[int]("price"),
	InStock: drel.NewBoolCol("in_stock"),
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://localhost:5432/drelexample?sslmode=disable"
	}

	ctx := context.Background()
	engine, err := drel.NewEngine(dsn, drel.WithContext(ctx))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer engine.Close()

	setup(ctx, engine)
	defer teardown(ctx, engine)

	repo := drel.NewRepository(engine, ProductMeta)

	// ─── QUERIES ────────────────────────────────────────────

	// Find by ID
	fmt.Println("=== Find by ID ===")
	p, err := repo.Find(ctx, 1)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  %s — $%d\n", p.Name, p.Price)

	// Where + OrderBy + Limit
	fmt.Println("\n=== In-stock products, cheapest first (limit 3) ===")
	products, err := repo.
		Where(Products.InStock.IsTrue()).
		OrderBy(Products.Price.Asc()).
		Limit(3).
		All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range products {
		fmt.Printf("  %s — $%d\n", p.Name, p.Price)
	}

	// OR condition
	fmt.Println("\n=== Price < $600 OR price > $2900 ===")
	products, err = repo.
		Where(drel.Or(
			Products.Price.LT(600),
			Products.Price.GT(2900),
		)).
		All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range products {
		fmt.Printf("  %s — $%d (in stock: %v)\n", p.Name, p.Price, p.InStock)
	}

	// Count + Exists
	fmt.Println("\n=== Aggregates ===")
	count, _ := repo.Count(ctx)
	fmt.Printf("  Total products: %d\n", count)

	inStockCount, _ := repo.Where(Products.InStock.IsTrue()).Count(ctx)
	fmt.Printf("  In stock: %d\n", inStockCount)

	hasExpensive, _ := repo.Where(Products.Price.GT(5000)).Exists(ctx)
	fmt.Printf("  Has product > $5000: %v\n", hasExpensive)

	// String contains
	fmt.Println("\n=== Name contains 'get' ===")
	products, err = repo.Where(Products.Name.Contains("get")).All(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range products {
		fmt.Printf("  %s\n", p.Name)
	}

	// ─── TRANSACTIONS ───────────────────────────────────────

	// INSERT
	fmt.Println("\n=== Insert via transaction ===")
	// WithTx is the form generated code uses: it puts the transaction in the
	// context, and MustFromContext reads it back. Engine.Transaction below is
	// the same machinery with the transaction passed by hand.
	err = engine.WithTx(ctx, func(ctx context.Context) error {
		txRepo := drel.NewTxRepository(drel.MustFromContext(ctx), ProductMeta)
		laptop := &Product{Name: "Laptop", Price: 99900, InStock: true}
		txRepo.Add(laptop)
		return nil // auto-flush + commit
	})
	if err != nil {
		log.Fatal(err)
	}

	all, _ := repo.All(ctx)
	fmt.Printf("  Products after insert: %d\n", len(all))
	for _, p := range all {
		fmt.Printf("    [%d] %s — $%d\n", p.ID, p.Name, p.Price)
	}

	// UPDATE (only changed fields)
	fmt.Println("\n=== Update price only (change tracking) ===")
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		txRepo := drel.NewTxRepository(tx, ProductMeta)
		laptop, err := txRepo.Find(ctx, 1)
		if err != nil {
			return err
		}
		fmt.Printf("  Before: %s — $%d\n", laptop.Name, laptop.Price)
		laptop.Price = 89900 // only this field changes
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	updated, _ := repo.Find(ctx, 1)
	fmt.Printf("  After:  %s — $%d\n", updated.Name, updated.Price)

	// DELETE
	fmt.Println("\n=== Delete via transaction ===")
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		txRepo := drel.NewTxRepository(tx, ProductMeta)
		laptop, err := txRepo.Find(ctx, 1)
		if err != nil {
			return err
		}
		return txRepo.Remove(laptop)
	})
	if err != nil {
		log.Fatal(err)
	}

	count, _ = repo.Count(ctx)
	fmt.Printf("  Products after delete: %d\n", count)

	// ROLLBACK
	fmt.Println("\n=== Rollback on error ===")

	engine.Transaction(ctx, func(tx *drel.Tx) error {
		txRepo := drel.NewTxRepository(tx, ProductMeta)
		txRepo.Add(&Product{Name: "Tablet", Price: 49900, InStock: true})
		return nil
	})

	countBefore, _ := repo.Count(ctx)
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		txRepo := drel.NewTxRepository(tx, ProductMeta)
		txRepo.Add(&Product{Name: "Ghost Product", Price: 1, InStock: false})
		return fmt.Errorf("something went wrong") // triggers rollback
	})
	fmt.Printf("  Error: %v\n", err)

	countAfter, _ := repo.Count(ctx)
	fmt.Printf("  Products before: %d, after rollback: %d (unchanged)\n", countBefore, countAfter)

	// MID-TX SAVECHANGES
	fmt.Println("\n=== Mid-transaction SaveChanges (get generated ID) ===")
	err = engine.Transaction(ctx, func(tx *drel.Tx) error {
		txRepo := drel.NewTxRepository(tx, ProductMeta)

		phone := &Product{Name: "Phone", Price: 79900, InStock: true}
		txRepo.Add(phone)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		fmt.Printf("  Phone inserted with ID: %d\n", phone.ID)

		case_ := &Product{Name: "Phone Case", Price: 2900, InStock: true}
		txRepo.Add(case_)
		return nil // second flush + commit
	})
	if err != nil {
		log.Fatal(err)
	}

	final, _ := repo.All(ctx)
	fmt.Printf("  Final product count: %d\n", len(final))
}

func setup(ctx context.Context, engine *drel.Engine) {
	engine.Exec(ctx, `DROP TABLE IF EXISTS products`)
	engine.Exec(ctx, `
		CREATE TABLE products (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			price INTEGER NOT NULL,
			in_stock BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	for _, p := range []struct {
		name    string
		price   int
		inStock bool
	}{
		{"Widget", 1000, true},
		{"Gadget", 2500, true},
		{"Doohickey", 500, false},
		{"Thingamajig", 1500, true},
		{"Whatchamacallit", 3000, false},
	} {
		engine.Exec(ctx, `INSERT INTO products (name, price, in_stock) VALUES ($1, $2, $3)`, p.name, p.price, p.inStock)
	}
}

func teardown(ctx context.Context, engine *drel.Engine) {
	engine.Exec(ctx, `DROP TABLE IF EXISTS products`)
}
