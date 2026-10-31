import os

os.chdir('/root/flashgramtest/gramsrv')

# Read router.go
with open('internal/rpc/router.go', 'r', errors='replace') as f:
    content = f.read()

# Find position
pos = content.find('r.registerPayments(d)')
if pos >= 0:
    # The new method to add
    new_method = """
// registerWallet registers wallet RPC handlers.
func (r *Router) registerWallet(d *tlprofile.Dispatcher) {
	d.Register(tlprofile.SemanticMethodWalletGetUserAddresses, func(ctx context.Context, object bin.Object) (any, error) {
		req, ok := object.(*tg.MTPwallet_GetUserAddresses)
		if !ok {
			return nil, errors.New("invalid request type")
		}

		force := req.Flags().IsFlagSet(0)
		var userIDs []int64
		if req.ID().Len() > 0 {
			for i := 0; i < req.ID().Len(); i++ {
				inputUser := req.ID().At(i)
				userIDs = append(userIDs, int64(inputUser.GetUserID()))
			}
		}

		ws := getWalletService(ctx)
		if ws == nil {
			return nil, errors.New("wallet service not available")
		}

		addrs, err := ws.GetUserAddresses(ctx, userIDs, force)
		if err != nil {
			return nil, err
		}

		var walletAddrs tg.MTPwallet_UserAddresses
		for _, addr := range addrs {
			walletAddr := tg.MTPWalletUserAddress{
				UserID: addr.UserID,
				Address: addr.Address,
			}
			walletAddrs.Data().Append(walletAddr)
		}

		return walletAddrs, nil
	})

	d.Register(tlprofile.SemanticMethodWalletSendTransaction, func(ctx context.Context, object bin.Object) (any, error) {
		req, ok := object.(*tg.MTPwallet_SendTransaction)
		if !ok {
			return nil, errors.New("invalid request type")
		}

		var amount *big.Int
		if req.Amount().Len() > 0 {
			amount = req.Amount().ToBigInt()
		}
		destination := req.Destination().String()
		_secretPhrase := req.SecretPhrase().String()

		ws := getWalletService(ctx)
		if ws == nil {
			return nil, errors.New("wallet service not available")
		}

		txID, err := ws.SendTransaction(ctx, amount, destination, _secretPhrase)
		if err != nil {
			return nil, err
		}

		return tg.MTPwallet_TransactionResult{
			TransactionID: txID,
		}, nil
	})
}"""

new_content = content[:pos] + new_method + content[pos:]
with open('internal/rpc/router.go', 'w', errors='replace') as f:
    f.write(new_content)
print('registerWallet method added successfully')