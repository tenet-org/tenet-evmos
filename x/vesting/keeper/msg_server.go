// Copyright 2022 Evmos Foundation
// This file is part of the Evmos Network packages.
//
// Evmos is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The Evmos packages are distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the Evmos packages. If not, see https://github.com/evmos/evmos/blob/main/LICENSE

package keeper

import (
	"context"
	"time"

	evmostypes "github.com/evmos/evmos/v15/types"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	errortypes "github.com/cosmos/cosmos-sdk/types/errors"
	vestingexported "github.com/cosmos/cosmos-sdk/x/auth/vesting/exported"
	sdkvesting "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"

	"github.com/evmos/evmos/v15/x/vesting/types"
)

var _ types.MsgServer = &Keeper{}

// CreateClawbackVestingAccount creates a new ClawbackVestingAccount, or merges
// a grant into an existing one.
func (k Keeper) CreateClawbackVestingAccount(
	goCtx context.Context,
	msg *types.MsgCreateClawbackVestingAccount,
) (*types.MsgCreateClawbackVestingAccountResponse, error) {
	return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "")
}

// Clawback removes the unvested amount from a ClawbackVestingAccount.
// The destination defaults to the funder address, but can be overridden.
func (k Keeper) Clawback(
	goCtx context.Context,
	msg *types.MsgClawback,
) (*types.MsgClawbackResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	ak := k.accountKeeper
	bk := k.bankKeeper

	// NOTE: ignore error in case dest address is not defined
	dest, _ := sdk.AccAddressFromBech32(msg.DestAddress)

	// NOTE: error checked during msg validation
	addr := sdk.MustAccAddressFromBech32(msg.AccountAddress)

	// Default destination to funder address
	if msg.DestAddress == "" {
		dest, _ = sdk.AccAddressFromBech32(msg.FunderAddress)
	}

	if bk.BlockedAddr(dest) {
		return nil, errorsmod.Wrapf(errortypes.ErrUnauthorized,
			"%s is not allowed to receive funds", msg.DestAddress,
		)
	}

	// Check if account exists
	acc := ak.GetAccount(ctx, addr)
	if acc == nil {
		return nil, errorsmod.Wrapf(errortypes.ErrNotFound, "account %s does not exist", msg.AccountAddress)
	}

	// Check if account has a clawback account
	va, ok := acc.(*types.ClawbackVestingAccount)
	if !ok {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "account not subject to clawback: %s", msg.AccountAddress)
	}

	// Check if account funder is same as in msg
	if va.FunderAddress != msg.FunderAddress {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "clawback can only be requested by original funder %s", va.FunderAddress)
	}

	// Return error if clawback is attempted before start time
	if ctx.BlockTime().Before(va.StartTime) {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "clawback can only be executed after vesting begins: %s", va.FunderAddress)
	}

	// Perform clawback transfer
	if err := k.transferClawback(ctx, *va, dest); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvents(
		sdk.Events{
			sdk.NewEvent(
				types.EventTypeClawback,
				sdk.NewAttribute(types.AttributeKeyFunder, msg.FunderAddress),
				sdk.NewAttribute(types.AttributeKeyAccount, msg.AccountAddress),
				sdk.NewAttribute(types.AttributeKeyDestination, msg.DestAddress),
			),
		},
	)

	return &types.MsgClawbackResponse{}, nil
}

// UpdateVestingFunder updates the funder account of a ClawbackVestingAccount.
func (k Keeper) UpdateVestingFunder(
	goCtx context.Context,
	msg *types.MsgUpdateVestingFunder,
) (*types.MsgUpdateVestingFunderResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	ak := k.accountKeeper
	bk := k.bankKeeper

	// NOTE: errors checked during msg validation
	newFunder := sdk.MustAccAddressFromBech32(msg.NewFunderAddress)
	vesting := sdk.MustAccAddressFromBech32(msg.VestingAddress)

	// Need to check if new funder can receive funds because in
	// Clawback function, destination defaults to funder address
	if bk.BlockedAddr(newFunder) {
		return nil, errorsmod.Wrapf(errortypes.ErrUnauthorized,
			"%s is not allowed to receive funds", msg.NewFunderAddress,
		)
	}

	// Check if vesting account exists
	vestingAcc := ak.GetAccount(ctx, vesting)
	if vestingAcc == nil {
		return nil, errorsmod.Wrapf(errortypes.ErrNotFound, "account %s does not exist", msg.VestingAddress)
	}

	// Check if account is a clawback vesting account
	va, ok := vestingAcc.(*types.ClawbackVestingAccount)
	if !ok {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "account not subject to clawback: %s", msg.VestingAddress)
	}

	// Check if account current funder is same as in msg
	if va.FunderAddress != msg.FunderAddress {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "clawback can only be requested by original funder %s", va.FunderAddress)
	}

	// Perform clawback account update
	va.FunderAddress = msg.NewFunderAddress
	// set the account with the updated funder
	ak.SetAccount(ctx, va)

	ctx.EventManager().EmitEvents(
		sdk.Events{
			sdk.NewEvent(
				types.EventTypeUpdateVestingFunder,
				sdk.NewAttribute(types.AttributeKeyFunder, msg.FunderAddress),
				sdk.NewAttribute(types.AttributeKeyAccount, msg.VestingAddress),
				sdk.NewAttribute(types.AttributeKeyNewFunder, msg.NewFunderAddress),
			),
		},
	)

	return &types.MsgUpdateVestingFunderResponse{}, nil
}

// ConvertVestingAccount converts a ClawbackVestingAccount to the default chain account
// after its lock and vesting periods have concluded.
func (k Keeper) ConvertVestingAccount(
	goCtx context.Context,
	msg *types.MsgConvertVestingAccount,
) (*types.MsgConvertVestingAccountResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	address := sdk.MustAccAddressFromBech32(msg.VestingAddress)
	account := k.accountKeeper.GetAccount(ctx, address)

	if account == nil {
		return nil, errorsmod.Wrapf(errortypes.ErrNotFound, "account %s does not exist", msg.VestingAddress)
	}

	// Check if account is of VestingAccount interface
	if _, ok := account.(vestingexported.VestingAccount); !ok {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "account not subject to vesting: %s", msg.VestingAddress)
	}

	// check if account is of type ClawbackVestingAccount
	vestingAcc, ok := account.(*types.ClawbackVestingAccount)
	if !ok {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "account %s is not a ClawbackVestingAccount", msg.VestingAddress)
	}

	// check if account  has any vesting coins left
	if vestingAcc.GetVestingCoins(ctx.BlockTime()) != nil {
		return nil, errorsmod.Wrapf(errortypes.ErrInvalidRequest, "vesting coins still left in account: %s", msg.VestingAddress)
	}

	ethAccount := evmostypes.ProtoAccount().(*evmostypes.EthAccount)
	ethAccount.BaseAccount = vestingAcc.BaseAccount
	k.accountKeeper.SetAccount(ctx, ethAccount)

	return &types.MsgConvertVestingAccountResponse{}, nil
}

// addGrant merges a new clawback vesting grant into an existing
// ClawbackVestingAccount.
func (k Keeper) addGrant(
	ctx sdk.Context,
	va *types.ClawbackVestingAccount,
	grantStartTime int64,
	grantLockupPeriods, grantVestingPeriods sdkvesting.Periods,
	grantCoins sdk.Coins,
) error {
	// how much is really delegated?
	bondedAmt := k.stakingKeeper.GetDelegatorBonded(ctx, va.GetAddress())
	unbondingAmt := k.stakingKeeper.GetDelegatorUnbonding(ctx, va.GetAddress())
	delegatedAmt := bondedAmt.Add(unbondingAmt)
	delegated := sdk.NewCoins(sdk.NewCoin(k.stakingKeeper.BondDenom(ctx), delegatedAmt))

	// modify schedules for the new grant
	newLockupStart, newLockupEnd, newLockupPeriods := types.DisjunctPeriods(va.GetStartTime(), grantStartTime, va.LockupPeriods, grantLockupPeriods)
	newVestingStart, newVestingEnd, newVestingPeriods := types.DisjunctPeriods(va.GetStartTime(), grantStartTime,
		va.GetVestingPeriods(), grantVestingPeriods)

	if newLockupStart != newVestingStart {
		return errorsmod.Wrapf(
			types.ErrVestingLockup,
			"vesting start time calculation should match lockup start (%d ≠ %d)",
			newVestingStart, newLockupStart,
		)
	}

	va.StartTime = time.Unix(newLockupStart, 0)
	va.EndTime = types.Max64(newLockupEnd, newVestingEnd)
	va.LockupPeriods = newLockupPeriods
	va.VestingPeriods = newVestingPeriods
	va.OriginalVesting = va.OriginalVesting.Add(grantCoins...)

	// cap DV at the current unvested amount, DF rounds out to current delegated
	unvested := va.GetVestingCoins(ctx.BlockTime())
	va.DelegatedVesting = delegated.Min(unvested)
	va.DelegatedFree = delegated.Sub(va.DelegatedVesting...)
	return nil
}

// transferClawback transfers unvested tokens in a ClawbackVestingAccount to
// dest address, updates the lockup schedule and removes future vesting events.
func (k Keeper) transferClawback(
	ctx sdk.Context,
	va types.ClawbackVestingAccount,
	dest sdk.AccAddress,
) error {
	// Compute clawback amount, unlock unvested tokens and remove future vesting events
	updatedAcc, toClawBack := va.ComputeClawback(ctx.BlockTime().Unix())
	if toClawBack.IsZero() {
		// no-op, nothing to transfer
		return nil
	}

	// set the account with the updated values of the vesting schedule
	k.accountKeeper.SetAccount(ctx, &updatedAcc)

	addr := updatedAcc.GetAddress()

	// NOTE: don't use `SpendableCoins` to get the minimum value to clawback since
	// the amount is retrieved from `ComputeClawback`, which ensures correctness.
	// `SpendableCoins` can result in gas exhaustion if the user has too many
	// different denoms (because of store iteration).

	// Transfer clawback to the destination (funder)
	return k.bankKeeper.SendCoins(ctx, addr, dest, toClawBack)
}
