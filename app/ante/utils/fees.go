package utils

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/types"
)

// DeductFees deducts fees from the given account.
func DeductFees(bankKeeper BankKeeper, fundRetriever func(ctx sdk.Context) (sdk.AccAddress, sdk.Dec), ctx sdk.Context, acc types.AccountI, fees sdk.Coins) error {
	if !fees.IsValid() {
		return sdkerrors.Wrapf(sdkerrors.ErrInsufficientFee, "invalid fee amount: %s", fees)
	}

	fundAddress, fundShare := fundRetriever(ctx)

	feesDec := sdk.NewDecCoinsFromCoins(fees...)
	toFund, _ := feesDec.MulDec(fundShare).TruncateDecimal()

	err := bankKeeper.SendCoinsFromAccountToModule(ctx, acc.GetAddress(), types.FeeCollectorName, fees)
	if err != nil {
		return sdkerrors.Wrapf(sdkerrors.ErrInsufficientFunds, err.Error())
	}

	if toFund.IsValid() && !toFund.IsZero() {
		err = bankKeeper.SendCoinsFromModuleToAccount(ctx, types.FeeCollectorName, fundAddress, toFund)
		if err != nil {
			return sdkerrors.Wrapf(sdkerrors.ErrInsufficientFunds, err.Error())
		}
	}

	return nil
}
