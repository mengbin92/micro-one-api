package biz

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
)

const defaultRechargeAmountMultiplier = 10

// RechargeAmountMultiplier is the USD wallet credit granted for one CNY.
func RechargeAmountMultiplier() float64 {
	rate, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("RECHARGE_AMOUNT_MULTIPLIER")), 64)
	if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return defaultRechargeAmountMultiplier
	}
	return rate
}

// WalletPriceAmount converts a nominal whole-CNY plan price to wallet units
// (1 USD = 10000 units), using the same multiplier as balance recharge.
func WalletPriceAmount(priceCNY int64) (int64, error) {
	return walletPriceAmount(priceCNY, RechargeAmountMultiplier())
}

// WalletAmountForMoneyCents converts a CNY payment amount with a frozen rate.
func WalletAmountForMoneyCents(moneyCents int64, rechargeMultiplier float64) (int64, error) {
	return convertedWalletAmount(moneyCents, 100, rechargeMultiplier)
}

func walletPriceAmount(priceCNY int64, rechargeMultiplier float64) (int64, error) {
	return convertedWalletAmount(priceCNY, 1, rechargeMultiplier)
}

func convertedWalletAmount(price int64, priceScale int64, rechargeMultiplier float64) (int64, error) {
	if price < 0 || rechargeMultiplier <= 0 || math.IsNaN(rechargeMultiplier) || math.IsInf(rechargeMultiplier, 0) {
		return 0, fmt.Errorf("invalid subscription price conversion")
	}
	// Read the rate's decimal representation, as persisted on frozen snapshots,
	// without converting the integer price to float64 or rounding its product.
	rate, _ := new(big.Rat).SetString(strconv.FormatFloat(rechargeMultiplier, 'g', -1, 64))
	amount := new(big.Rat).Mul(new(big.Rat).SetInt64(price), rate)
	amount.Mul(amount, big.NewRat(10000, priceScale))
	rounded, remainder := new(big.Int), new(big.Int)
	rounded.QuoRem(amount.Num(), amount.Denom(), remainder)
	if remainder.Lsh(remainder, 1).Cmp(amount.Denom()) >= 0 {
		rounded.Add(rounded, big.NewInt(1))
	}
	if !rounded.IsInt64() || (price > 0 && rounded.Sign() <= 0) {
		return 0, fmt.Errorf("subscription wallet price is out of range")
	}
	return rounded.Int64(), nil
}

// PriceConversion freezes the wallet equivalent of a new CNY payment order.
// A missing conversion on a historical snapshot retains its original meaning.
type PriceConversion struct {
	Version            int32   `json:"version"`
	RechargeMultiplier float64 `json:"recharge_multiplier"`
	WalletAmount       int64   `json:"wallet_amount"`
}

func CapturePriceConversion(priceCNY int64) (*PriceConversion, error) {
	if priceCNY <= 0 {
		return nil, fmt.Errorf("invalid subscription price conversion")
	}
	rate := RechargeAmountMultiplier()
	amount, err := walletPriceAmount(priceCNY, rate)
	if err != nil {
		return nil, err
	}
	return &PriceConversion{Version: 1, RechargeMultiplier: rate, WalletAmount: amount}, nil
}

func (p *PriceConversion) validate(priceCNY int64) error {
	if p == nil {
		return nil
	}
	if p.Version != 1 || priceCNY <= 0 {
		return fmt.Errorf("invalid subscription price conversion")
	}
	amount, err := walletPriceAmount(priceCNY, p.RechargeMultiplier)
	if err != nil || p.WalletAmount != amount {
		return fmt.Errorf("invalid subscription price conversion")
	}
	return nil
}
