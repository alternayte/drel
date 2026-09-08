package accounts

import (
	"fmt"
	"strconv"

	"github.com/alternayte/drel"
)

// Money is a multi-column value object mapping to amount + currency.
// Sub-column values must be comparable primitives (int, string here) so the
// generated per-sub-column diff works.
type Money struct {
	amount   int
	currency string
}

func NewMoney(amount int, currency string) Money {
	return Money{amount: amount, currency: currency}
}

func (m Money) Amount() int      { return m.amount }
func (m Money) Currency() string { return m.currency }

// drel.MultiColumnMapper.
func (m Money) DrelColumns() []string      { return []string{"amount", "currency"} }
func (m Money) DrelValues() ([]any, error) { return []any{m.amount, m.currency}, nil }
func (m *Money) DrelScanMulti(v []any) error {
	if len(v) != 2 {
		return nil
	}
	switch a := v[0].(type) {
	case int64:
		m.amount = int(a)
	case int:
		m.amount = a
	case string:
		// SQLite TEXT column: amount was stored as text.
		n, err := strconv.Atoi(a)
		if err != nil {
			return fmt.Errorf("money: parse amount %q: %w", a, err)
		}
		m.amount = n
	}
	if c, ok := v[1].(string); ok {
		m.currency = c
	}
	return nil
}

type Account struct {
	drel.Model[int]
	owner   string `db:"owner"`
	balance Money  `db:"balance_amount,balance_currency"`
}

func NewAccount(owner string, balance Money) *Account {
	a := &Account{owner: owner, balance: balance}
	return a
}

func (a *Account) Owner() string      { return a.owner }
func (a *Account) Balance() Money     { return a.balance }
func (a *Account) SetBalance(m Money) { a.balance = m }
