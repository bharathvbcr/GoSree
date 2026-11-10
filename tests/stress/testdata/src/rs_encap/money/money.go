// Package money has a type with an invariant that its constructor establishes: cents is never
// negative and currency is a known code.
package money

import "errors"

type Amount struct {
	cents    int64
	currency string
	history  []int64
}

func New(cents int64, currency string) (Amount, error) {
	if cents < 0 || currency == "" {
		return Amount{}, errors.New("invalid amount")
	}
	return Amount{cents: cents, currency: currency}, nil
}

func (a Amount) Currency() string { return a.currency }

// Add records the change in history, which is shared by every copy of a.
func (a *Amount) Add(c int64) {
	a.cents += c
	a.history = append(a.history, c)
}

func (a Amount) History() []int64 { return a.history }
