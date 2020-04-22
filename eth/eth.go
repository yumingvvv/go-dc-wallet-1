package eth

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"go-dc-wallet/app"
	"go-dc-wallet/app/model"
	"go-dc-wallet/ethclient"
	"go-dc-wallet/hcommon"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/shopspring/decimal"
)

// CheckAddressCheck 检测是否有充足的备用地址
func CheckAddressCheck() {
	lockKey := "EthCheckAddressCheck"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	// 获取配置 允许的最小剩余地址数
	minFreeRow, err := app.SQLGetTAppConfigIntByK(
		context.Background(),
		app.DbCon,
		"min_free_address",
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAppConfigInt err: [%T] %s", err, err.Error())
		return
	}
	if minFreeRow == nil {
		hcommon.Log.Errorf("no config int of min_free_address")
		return
	}
	// 获取当前剩余可用地址数
	freeCount, err := app.SQLGetTAddressKeyFreeCount(
		context.Background(),
		app.DbCon,
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAddressKeyFreeCount err: [%T] %s", err, err.Error())
		return
	}
	// 如果数据库中剩余可用地址小于最小允许可用地址
	if freeCount < minFreeRow.V {
		var rows []*model.DBTAddressKey
		// 遍历差值次数
		for i := int64(0); i < minFreeRow.V-freeCount; i++ {
			// 生成私钥
			privateKey, err := crypto.GenerateKey()
			if err != nil {
				hcommon.Log.Warnf("GenerateKey err: [%T] %s", err, err.Error())
				return
			}
			privateKeyBytes := crypto.FromECDSA(privateKey)
			privateKeyStr := hexutil.Encode(privateKeyBytes)
			// 加密密钥
			privateKeyStrEn := hcommon.AesEncrypt(privateKeyStr, app.Cfg.AESKey)
			// 获取地址
			publicKey := privateKey.Public()
			publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
			if !ok {
				hcommon.Log.Warnf("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
				return
			}
			// 地址全部储存为小写方便处理
			address := strings.ToLower(crypto.PubkeyToAddress(*publicKeyECDSA).Hex())
			// 存入待添加队列
			rows = append(rows, &model.DBTAddressKey{
				Address: address,
				Pwd:     privateKeyStrEn,
				UseTag:  0,
			})
		}
		// 一次性将生成的地址存入数据库
		_, err = model.SQLCreateIgnoreManyTAddressKey(
			context.Background(),
			app.DbCon,
			rows,
		)
		if err != nil {
			hcommon.Log.Warnf("SQLCreateIgnoreManyTAddressKey err: [%T] %s", err, err.Error())
			return
		}
	}
}

// CheckBlockSeek 检测到账
func CheckBlockSeek() {
	lockKey := "EthCheckBlockSeek"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	// 获取配置 延迟确认数
	confirmRow, err := app.SQLGetTAppConfigIntByK(
		context.Background(),
		app.DbCon,
		"block_confirm_num",
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAppConfigInt err: [%T] %s", err, err.Error())
		return
	}
	if confirmRow == nil {
		hcommon.Log.Errorf("no config int of min_free_address")
		return
	}
	// 获取状态 当前处理完成的最新的block number
	seekRow, err := app.SQLGetTAppStatusIntByK(
		context.Background(),
		app.DbCon,
		"seek_num",
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAppStatusIntByK err: [%T] %s", err, err.Error())
		return
	}
	if seekRow == nil {
		hcommon.Log.Errorf("no config int of seek_num")
		return
	}
	// rpc 获取当前最新区块数
	rpcBlockNum, err := ethclient.RpcBlockNumber(context.Background())
	if err != nil {
		hcommon.Log.Warnf("RpcBlockNumber err: [%T] %s", err, err.Error())
		return
	}
	startI := seekRow.V + 1
	endI := rpcBlockNum - confirmRow.V
	if startI < endI {
		// 手续费钱包列表
		feeRow, err := app.SQLGetTAppConfigStrByK(
			context.Background(),
			app.DbCon,
			"fee_wallet_address_list",
		)
		if err != nil {
			hcommon.Log.Warnf("SQLGetTAppConfigInt err: [%T] %s", err, err.Error())
			return
		}
		if feeRow == nil {
			hcommon.Log.Errorf("no config int of fee_wallet_address_list")
			return
		}
		addresses := strings.Split(feeRow.V, ",")
		var feeAddresses []string
		for _, address := range addresses {
			if address == "" {
				continue
			}
			feeAddresses = append(feeAddresses, address)
		}
		// 遍历获取需要查询的block信息
		for i := startI; i < endI; i++ {
			// rpc获取block信息
			rpcBlock, err := ethclient.RpcBlockByNum(context.Background(), i)
			if err != nil {
				hcommon.Log.Warnf("EthRpcBlockByNum err: [%T] %s", err, err.Error())
				return
			}
			// 接收地址列表
			var toAddresses []string
			// map[接收地址] => []交易信息
			toAddressTxMap := make(map[string][]*types.Transaction)

			for _, rpcTx := range rpcBlock.Transactions() {
				// 转账数额大于0 and 不是创建合约交易
				if rpcTx.Value().Int64() > 0 && rpcTx.To() != nil {
					msg, err := rpcTx.AsMessage(types.NewEIP155Signer(rpcTx.ChainId()))
					if err != nil {
						hcommon.Log.Errorf("AsMessage err: [%T] %s", err, err.Error())
					}
					if hcommon.IsStringInSlice(feeAddresses, strings.ToLower(msg.From().Hex())) {
						// 如果打币地址在手续费热钱包地址则不处理
						continue
					}
					toAddress := strings.ToLower(rpcTx.To().Hex())
					toAddressTxMap[toAddress] = append(toAddressTxMap[toAddress], rpcTx)
					toAddresses = append(toAddresses, toAddress)
				}
			}
			// 从db中查询这些地址是否是冲币地址中的地址
			dbAddressRows, err := app.SQLSelectTAddressKeyColByAddress(
				context.Background(),
				app.DbCon,
				[]string{
					model.DBColTAddressKeyAddress,
					model.DBColTAddressKeyUseTag,
				},
				toAddresses,
			)
			if err != nil {
				hcommon.Log.Warnf("dbAddressRows err: [%T] %s", err, err.Error())
				return
			}
			// 待插入数据
			var dbTxRows []*model.DBTTx
			// map[接收地址] => 产品id
			addressProductMap := make(map[string]int64)
			for _, dbAddressRow := range dbAddressRows {
				addressProductMap[dbAddressRow.Address] = dbAddressRow.UseTag
			}
			// 时间
			now := time.Now().Unix()
			// 遍历数据库中有交易的地址
			for _, dbAddressRow := range dbAddressRows {
				// 获取地址对应的交易列表
				txes := toAddressTxMap[dbAddressRow.Address]
				for _, tx := range txes {
					msg, err := tx.AsMessage(types.NewEIP155Signer(tx.ChainId()))
					if err != nil {
						hcommon.Log.Errorf("AsMessage err: [%T] %s", err, err.Error())
					}
					toAddress := strings.ToLower(tx.To().Hex())
					balanceReal := decimal.NewFromInt(tx.Value().Int64()).Div(decimal.NewFromInt(1e18))
					dbTxRows = append(dbTxRows, &model.DBTTx{
						ProductID:    addressProductMap[toAddress],
						TxID:         tx.Hash().String(),
						FromAddress:  strings.ToLower(msg.From().Hex()),
						ToAddress:    toAddress,
						Balance:      tx.Value().Int64(),
						BalanceReal:  balanceReal.String(),
						CreateTime:   now,
						HandleStatus: 0,
						HandleMsg:    "",
						HandleTime:   now,
					})
				}
			}
			// 插入交易数据
			_, err = model.SQLCreateIgnoreManyTTx(
				context.Background(),
				app.DbCon,
				dbTxRows,
			)
			if err != nil {
				hcommon.Log.Warnf("SQLCreateIgnoreManyTTx err: [%T] %s", err, err.Error())
				return
			}
			// 更新检查到的最新区块数
			_, err = app.SQLUpdateTAppStatusIntByK(
				context.Background(),
				app.DbCon,
				&model.DBTAppStatusInt{
					K: "seek_num",
					V: i,
				},
			)
			if err != nil {
				hcommon.Log.Warnf("SQLUpdateTAppStatusIntByK err: [%T] %s", err, err.Error())
				return
			}
		}
	}
}

// CheckAddressOrg 零钱整理到冷钱包
func CheckAddressOrg() {
	lockKey := "EthCheckAddressOrg"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	// 获取冷钱包地址
	coldRow, err := app.SQLGetTAppConfigStrByK(
		context.Background(),
		app.DbCon,
		"cold_wallet_address",
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAppConfigInt err: [%T] %s", err, err.Error())
		return
	}
	if coldRow == nil {
		hcommon.Log.Errorf("no config int of cold_wallet_address")
		return
	}
	re := regexp.MustCompile("^0x[0-9a-fA-F]{40}$")
	if !re.MatchString(coldRow.V) {
		hcommon.Log.Errorf("config int cold_wallet_address err: %s", coldRow.V)
		return
	}
	coldAddress := common.HexToAddress(coldRow.V)
	hcommon.Log.Debugf("coldAddress: %s", coldAddress)
	// 获取待整理的地址列表
	txRows, err := app.SQLSelectTTxColByOrg(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTTxID,
			model.DBColTTxToAddress,
			model.DBColTTxBalance,
		},
	)
	if err != nil {
		hcommon.Log.Warnf("SQLSelectTTxColByOrg err: [%T] %s", err, err.Error())
		return
	}
	// 将待整理地址按地址做归并处理
	type AddressInfo struct {
		RowIDs  []int64
		Balance int64
	}
	addressMap := make(map[string]*AddressInfo)
	// 获取gap price
	gasPrice := int64(0)
	if len(txRows) > 0 {
		gasRow, err := app.SQLGetTAppStatusIntByK(
			context.Background(),
			app.DbCon,
			"to_cold_gap_price",
		)
		if err != nil {
			hcommon.Log.Warnf("SQLGetTAppStatusIntByK err: [%T] %s", err, err.Error())
			return
		}
		if gasRow == nil {
			hcommon.Log.Errorf("no config int of to_cold_gap_price")
			return
		}
		gasPrice = gasRow.V
	}
	gasLimit := int64(21000)
	feeValue := gasLimit * gasPrice
	var addresses []string
	for _, txRow := range txRows {
		info := addressMap[txRow.ToAddress]
		if info == nil {
			info = &AddressInfo{
				RowIDs:  []int64{},
				Balance: 0,
			}
			addressMap[txRow.ToAddress] = info
		}
		info.RowIDs = append(info.RowIDs, txRow.ID)
		info.Balance += txRow.Balance

		addresses = append(addresses, txRow.ToAddress)
	}
	now := time.Now().Unix()
	var sendRows []*model.DBTSend
	for address, info := range addressMap {
		// 获取私钥
		keyRow, err := app.SQLGetTAddressKeyColByAddress(
			context.Background(),
			app.DbCon,
			[]string{
				model.DBColTAddressKeyPwd,
			},
			address,
		)
		if err != nil {
			hcommon.Log.Warnf("SQLGetTAddressKeyColByAddress err: [%T] %s", err, err.Error())
			return
		}
		if keyRow == nil {
			hcommon.Log.Errorf("no key of: %s", address)
			return
		}
		key := hcommon.AesDecrypt(keyRow.Pwd, app.Cfg.AESKey)
		if len(key) == 0 {
			hcommon.Log.Errorf("error key of: %s", address)
			return
		}
		if strings.HasPrefix(key, "0x") {
			key = key[2:]
		}
		privateKey, err := crypto.HexToECDSA(key)
		if err != nil {
			hcommon.Log.Warnf("HexToECDSA err: [%T] %s", err, err.Error())
			return
		}
		// 获取nonce值
		nonce, err := GetNonce(app.DbCon, address)
		if err != nil {
			hcommon.Log.Warnf("GetNonce err: [%T] %s", err, err.Error())
			return
		}
		// 发送数量
		sendBalance := info.Balance - feeValue
		if sendBalance <= 0 {
			continue
		}
		sendBalanceReal := decimal.NewFromInt(sendBalance).Div(decimal.NewFromInt(1e18))
		// 生成tx
		var data []byte
		tx := types.NewTransaction(
			uint64(nonce),
			coldAddress,
			big.NewInt(sendBalance),
			uint64(gasLimit),
			big.NewInt(gasPrice),
			data,
		)
		chainID, err := ethclient.RpcNetworkID(context.Background())
		if err != nil {
			hcommon.Log.Warnf("RpcNetworkID err: [%T] %s", err, err.Error())
			return
		}
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), privateKey)
		if err != nil {
			hcommon.Log.Warnf("RpcNetworkID err: [%T] %s", err, err.Error())
			return
		}
		ts := types.Transactions{signedTx}
		rawTxBytes := ts.GetRlp(0)
		rawTxHex := hex.EncodeToString(rawTxBytes)
		txHash := strings.ToLower(signedTx.Hash().Hex())
		// 创建存入数据
		for rowIndex, rowID := range info.RowIDs {
			if rowIndex == 0 {
				sendRows = append(sendRows, &model.DBTSend{
					RelatedType:  1,
					RelatedID:    rowID,
					TxID:         txHash,
					FromAddress:  address,
					ToAddress:    coldRow.V,
					Balance:      sendBalance,
					BalanceReal:  sendBalanceReal.String(),
					Gas:          gasLimit,
					GasPrice:     gasPrice,
					Nonce:        nonce,
					Hex:          rawTxHex,
					CreateTime:   now,
					HandleStatus: 0,
					HandleMsg:    "",
					HandleTime:   now,
				})
			} else {
				sendRows = append(sendRows, &model.DBTSend{
					RelatedType:  1,
					RelatedID:    rowID,
					TxID:         txHash,
					FromAddress:  address,
					ToAddress:    coldRow.V,
					Balance:      0,
					BalanceReal:  "",
					Gas:          0,
					GasPrice:     0,
					Nonce:        -1,
					Hex:          "",
					CreateTime:   now,
					HandleStatus: 0,
					HandleMsg:    "",
					HandleTime:   now,
				})
			}
		}
	}
	// 用事物处理数据
	if len(addresses) > 0 {
		isComment := false
		tx, err := app.DbCon.BeginTxx(context.Background(), nil)
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		defer func() {
			if !isComment {
				_ = tx.Rollback()
			}
		}()
		// 更改状态
		_, err = app.SQLUpdateTTxOrgStatusByAddresses(
			context.Background(),
			tx,
			addresses,
			model.DBTTx{
				OrgStatus: 1,
				OrgMsg:    "gen raw tx",
				OrgTime:   now,
			},
		)
		// 插入数据
		_, err = model.SQLCreateManyTSend(
			context.Background(),
			tx,
			sendRows,
		)
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		err = tx.Commit()
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		isComment = true
	}
}

// CheckRawTxSend 发送交易
func CheckRawTxSend() {
	lockKey := "EthCheckRawTxSend"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	sendRows, err := app.SQLSelectTSendColByStatus(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTSendID,
			model.DBColTSendTxID,
			model.DBColTSendHex,
			model.DBColTSendRelatedType,
			model.DBColTSendRelatedID,
		},
		0,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}

	var withdrawIDs []int64
	withdrawMap := make(map[int64]*model.DBTWithdraw)
	for _, sendRow := range sendRows {
		if sendRow.RelatedType == 2 {
			// 提币
			if !hcommon.IsIntInSlice(withdrawIDs, sendRow.RelatedID) {
				withdrawIDs = append(withdrawIDs, sendRow.RelatedID)
			}

		}
	}
	withdrawRows, err := model.SQLSelectTWithdrawCol(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTWithdrawID,
			model.DBColTWithdrawProductID,
			model.DBColTWithdrawOutSerial,
			model.DBColTWithdrawToAddress,
			model.DBColTWithdrawBalanceReal,
		},
		withdrawIDs,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	var productIDs []int64
	sendRowProductMap := make(map[int64]int64)
	for _, withdrawRow := range withdrawRows {
		withdrawMap[withdrawRow.ID] = withdrawRow
		sendRowProductMap[withdrawRow.ID] = withdrawRow.ProductID
		if !hcommon.IsIntInSlice(productIDs, withdrawRow.ProductID) {
			productIDs = append(productIDs, withdrawRow.ProductID)
		}
	}
	productRows, err := model.SQLSelectTProductCol(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTProductID,
			model.DBColTProductAppName,
			model.DBColTProductCbURL,
			model.DBColTProductAppSk,
		},
		productIDs,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	productMap := make(map[int64]*model.DBTProduct)
	for _, productRow := range productRows {
		productMap[productRow.ID] = productRow
	}
	var txIDs []string
	var notifyRows []*model.DBTProductNotify
	now := time.Now().Unix()
	for _, sendRow := range sendRows {
		rawTxBytes, err := hex.DecodeString(sendRow.Hex)
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		tx := new(types.Transaction)
		err = rlp.DecodeBytes(rawTxBytes, &tx)
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		err = ethclient.RpcSendTransaction(
			context.Background(),
			tx,
		)
		if err != nil {
			if !strings.Contains(err.Error(), "known transaction") {
				hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
				return
			}
		}
		if sendRow.RelatedType == 2 {
			// 通知
			withdrawRow := withdrawMap[sendRow.RelatedID]
			productRow := productMap[withdrawRow.ProductID]
			nonce := hcommon.GetUUIDStr()
			reqObj := gin.H{
				"tx_hash":     sendRow.TxID,
				"balance":     withdrawRow.BalanceReal,
				"app_name":    productRow.AppName,
				"out_serial":  withdrawRow.OutSerial,
				"address":     withdrawRow.ToAddress,
				"symbol":      "eth",
				"notify_type": 2,
			}
			reqObj["sign"] = hcommon.GetSign(productRow.AppSk, reqObj)
			req, err := json.Marshal(reqObj)
			if err != nil {
				hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
				return
			}
			notifyRows = append(notifyRows, &model.DBTProductNotify{
				Nonce:        nonce,
				ProductID:    withdrawRow.ProductID,
				ItemType:     2,
				ItemID:       withdrawRow.ID,
				NotifyType:   2,
				URL:          productRow.CbURL,
				Msg:          string(req),
				HandleStatus: 0,
				HandleMsg:    "",
				CreateTime:   now,
				UpdateTime:   now,
			})
		}
		txIDs = append(txIDs, strings.ToLower(tx.Hash().Hex()))
	}
	_, err = model.SQLCreateIgnoreManyTProductNotify(
		context.Background(),
		app.DbCon,
		notifyRows,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	_, err = app.SQLUpdateTWithdrawStatusByTxIDs(
		context.Background(),
		app.DbCon,
		txIDs,
		model.DBTWithdraw{
			HandleStatus: 1,
			HandleMsg:    "send",
			HandleTime:   now,
		},
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	_, err = app.SQLUpdateTSendStatusByTxIDs(
		context.Background(),
		app.DbCon,
		txIDs,
		model.DBTSend{
			HandleStatus: 1,
			HandleMsg:    "send",
			HandleTime:   now,
		},
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
}

// CheckRawTxConfirm 确认tx是否打包完成
func CheckRawTxConfirm() {
	lockKey := "EthCheckRawTxConfirm"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	sendRows, err := app.SQLSelectTSendColByStatus(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTSendRelatedType,
			model.DBColTSendRelatedID,
			model.DBColTSendID,
			model.DBColTSendTxID,
		},
		1,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	var withdrawIDs []int64
	withdrawMap := make(map[int64]*model.DBTWithdraw)
	for _, sendRow := range sendRows {
		if sendRow.RelatedType == 2 {
			// 提币
			if !hcommon.IsIntInSlice(withdrawIDs, sendRow.RelatedID) {
				withdrawIDs = append(withdrawIDs, sendRow.RelatedID)
			}

		}
	}
	withdrawRows, err := model.SQLSelectTWithdrawCol(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTWithdrawID,
			model.DBColTWithdrawProductID,
			model.DBColTWithdrawOutSerial,
			model.DBColTWithdrawToAddress,
			model.DBColTWithdrawBalanceReal,
		},
		withdrawIDs,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	var productIDs []int64
	sendRowProductMap := make(map[int64]int64)
	for _, withdrawRow := range withdrawRows {
		withdrawMap[withdrawRow.ID] = withdrawRow
		sendRowProductMap[withdrawRow.ID] = withdrawRow.ProductID
		if !hcommon.IsIntInSlice(productIDs, withdrawRow.ProductID) {
			productIDs = append(productIDs, withdrawRow.ProductID)
		}
	}
	productRows, err := model.SQLSelectTProductCol(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTProductID,
			model.DBColTProductAppName,
			model.DBColTProductCbURL,
			model.DBColTProductAppSk,
		},
		productIDs,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	productMap := make(map[int64]*model.DBTProduct)
	for _, productRow := range productRows {
		productMap[productRow.ID] = productRow
	}
	var notifyRows []*model.DBTProductNotify
	now := time.Now().Unix()

	var sendIDs []int64
	for _, sendRow := range sendRows {
		rpcTx, err := ethclient.RpcTransactionByHash(
			context.Background(),
			sendRow.TxID,
		)
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		if rpcTx == nil {
			return
		}
		// 完成
		sendIDs = append(sendIDs, sendRow.ID)
		if sendRow.RelatedType == 2 {
			// 通知
			withdrawRow := withdrawMap[sendRow.RelatedID]
			productRow := productMap[withdrawRow.ProductID]
			nonce := hcommon.GetUUIDStr()
			reqObj := gin.H{
				"tx_hash":     sendRow.TxID,
				"balance":     withdrawRow.BalanceReal,
				"app_name":    productRow.AppName,
				"out_serial":  withdrawRow.OutSerial,
				"address":     withdrawRow.ToAddress,
				"symbol":      "eth",
				"notify_type": 3,
			}
			reqObj["sign"] = hcommon.GetSign(productRow.AppSk, reqObj)
			req, err := json.Marshal(reqObj)
			if err != nil {
				hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
				return
			}
			notifyRows = append(notifyRows, &model.DBTProductNotify{
				Nonce:        nonce,
				ProductID:    withdrawRow.ProductID,
				ItemType:     2,
				ItemID:       withdrawRow.ID,
				NotifyType:   3,
				URL:          productRow.CbURL,
				Msg:          string(req),
				HandleStatus: 0,
				HandleMsg:    "",
				CreateTime:   now,
				UpdateTime:   now,
			})
			// 提币
			_, err = app.SQLUpdateTWithdrawGenTx(
				context.Background(),
				app.DbCon,
				&model.DBTWithdraw{
					ID:           sendRow.RelatedID,
					TxHash:       sendRow.TxID,
					HandleStatus: 2,
					HandleMsg:    "confirmed",
					HandleTime:   now,
				},
			)
			if err != nil {
				hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
				return
			}
		}
	}
	_, err = model.SQLCreateIgnoreManyTProductNotify(
		context.Background(),
		app.DbCon,
		notifyRows,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	_, err = app.SQLUpdateTSendStatusByIDs(
		context.Background(),
		app.DbCon,
		sendIDs,
		model.DBTSend{
			HandleStatus: 2,
			HandleMsg:    "confirmed",
			HandleTime:   now,
		},
	)
}

// CheckWithdraw 检测提现
func CheckWithdraw() {
	lockKey := "EthCheckWithdraw"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	// 获取热钱包地址
	hotRow, err := app.SQLGetTAppConfigStrByK(
		context.Background(),
		app.DbCon,
		"hot_wallet_address",
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAppConfigInt err: [%T] %s", err, err.Error())
		return
	}
	if hotRow == nil {
		hcommon.Log.Errorf("no config int of hot_wallet_address")
		return
	}
	re := regexp.MustCompile("^0x[0-9a-fA-F]{40}$")
	if !re.MatchString(hotRow.V) {
		hcommon.Log.Errorf("config int hot_wallet_address err: %s", hotRow.V)
		return
	}
	hotAddress := common.HexToAddress(hotRow.V)
	hcommon.Log.Debugf("hotAddress: %s", hotAddress)
	// 获取私钥
	keyRow, err := app.SQLGetTAddressKeyColByAddress(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTAddressKeyPwd,
		},
		hotRow.V,
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAddressKeyColByAddress err: [%T] %s", err, err.Error())
		return
	}
	if keyRow == nil {
		hcommon.Log.Errorf("no key of: %s", hotRow.V)
		return
	}
	key := hcommon.AesDecrypt(keyRow.Pwd, app.Cfg.AESKey)
	if len(key) == 0 {
		hcommon.Log.Errorf("error key of: %s", hotRow.V)
		return
	}
	if strings.HasPrefix(key, "0x") {
		key = key[2:]
	}
	privateKey, err := crypto.HexToECDSA(key)
	if err != nil {
		hcommon.Log.Warnf("HexToECDSA err: [%T] %s", err, err.Error())
		return
	}
	hcommon.Log.Debugf("privateKey: %v", privateKey)
	withdrawRows, err := app.SQLSelectTWithdrawColByStatus(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTWithdrawID,
		},
		0,
	)
	if err != nil {
		hcommon.Log.Warnf("SQLSelectTWithdrawColByStatus err: [%T] %s", err, err.Error())
		return
	}
	if len(withdrawRows) == 0 {
		return
	}
	hotAddressBalance, err := ethclient.RpcBalanceAt(
		context.Background(),
		hotRow.V,
	)
	if err != nil {
		hcommon.Log.Warnf("RpcBalanceAt err: [%T] %s", err, err.Error())
		return
	}
	pendingBalance, err := app.SQLGetTSendPendingBalance(
		context.Background(),
		app.DbCon,
		hotRow.V,
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTSendPendingBalance err: [%T] %s", err, err.Error())
		return
	}
	hotAddressBalance -= pendingBalance
	hcommon.Log.Debugf("hotAddressBalance: %d", hotAddressBalance)
	// 获取gap price
	gasRow, err := app.SQLGetTAppStatusIntByK(
		context.Background(),
		app.DbCon,
		"to_user_gap_price",
	)
	if err != nil {
		hcommon.Log.Warnf("SQLGetTAppStatusIntByK err: [%T] %s", err, err.Error())
		return
	}
	if gasRow == nil {
		hcommon.Log.Errorf("no config int of to_user_gap_price")
		return
	}
	gasPrice := gasRow.V
	gasLimit := int64(21000)
	feeValue := gasLimit * gasPrice
	chainID, err := ethclient.RpcNetworkID(context.Background())
	if err != nil {
		hcommon.Log.Warnf("RpcNetworkID err: [%T] %s", err, err.Error())
		return
	}

	for _, withdrawRow := range withdrawRows {
		err = handleWithdraw(withdrawRow.ID, chainID, hotRow.V, privateKey, &hotAddressBalance, gasLimit, gasPrice, feeValue)
		if err != nil {
			hcommon.Log.Warnf("RpcBalanceAt err: [%T] %s", err, err.Error())
			return
		}
	}
}

func handleWithdraw(withdrawID int64, chainID int64, hotAddress string, privateKey *ecdsa.PrivateKey, hotAddressBalance *int64, gasLimit, gasPrice, feeValue int64) error {
	isComment := false
	dbTx, err := app.DbCon.BeginTxx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() {
		if !isComment {
			_ = dbTx.Rollback()
		}
	}()
	// 处理业务
	withdrawRow, err := app.SQLGetTWithdrawColForUpdate(
		context.Background(),
		dbTx,
		[]string{
			model.DBColTWithdrawID,
			model.DBColTWithdrawBalanceReal,
			model.DBColTWithdrawToAddress,
		},
		withdrawID,
		0,
	)
	if err != nil {
		return err
	}
	if withdrawRow == nil {
		return nil
	}
	balanceObj, err := decimal.NewFromString(withdrawRow.BalanceReal)
	if err != nil {
		return err
	}
	balance := balanceObj.Mul(decimal.NewFromInt(1e18)).IntPart()
	hcommon.Log.Debugf("balance: %d", balance)
	*hotAddressBalance -= balance + feeValue
	if *hotAddressBalance < 0 {
		hcommon.Log.Warnf("hot balance limit")
		return nil
	}
	// nonce
	nonce, err := GetNonce(
		dbTx,
		hotAddress,
	)
	if err != nil {
		return err
	}
	// 创建交易
	var data []byte
	tx := types.NewTransaction(
		uint64(nonce),
		common.HexToAddress(withdrawRow.ToAddress),
		big.NewInt(balance),
		uint64(gasLimit),
		big.NewInt(gasPrice),
		data,
	)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(big.NewInt(chainID)), privateKey)
	if err != nil {
		return err
	}
	ts := types.Transactions{signedTx}
	rawTxBytes := ts.GetRlp(0)
	rawTxHex := hex.EncodeToString(rawTxBytes)
	txHash := strings.ToLower(signedTx.Hash().Hex())
	now := time.Now().Unix()
	_, err = app.SQLUpdateTWithdrawGenTx(
		context.Background(),
		dbTx,
		&model.DBTWithdraw{
			ID:           withdrawID,
			TxHash:       txHash,
			HandleStatus: 1,
			HandleMsg:    "gen tx hex",
			HandleTime:   now,
		},
	)
	if err != nil {
		return err
	}
	_, err = model.SQLCreateTSend(
		context.Background(),
		dbTx,
		&model.DBTSend{
			RelatedType:  2,
			RelatedID:    withdrawID,
			TxID:         txHash,
			FromAddress:  hotAddress,
			ToAddress:    withdrawRow.ToAddress,
			Balance:      balance,
			BalanceReal:  withdrawRow.BalanceReal,
			Gas:          gasLimit,
			GasPrice:     gasPrice,
			Nonce:        nonce,
			Hex:          rawTxHex,
			HandleStatus: 0,
			HandleMsg:    "init",
			HandleTime:   now,
		},
	)
	if err != nil {
		return err
	}
	// 处理完成
	err = dbTx.Commit()
	if err != nil {
		return err
	}
	isComment = true
	return nil
}

func CheckTxNotify() {
	lockKey := "CheckTxNotify"
	ok, err := app.GetLock(
		context.Background(),
		app.DbCon,
		lockKey,
	)
	if err != nil {
		hcommon.Log.Warnf("GetLock err: [%T] %s", err, err.Error())
		return
	}
	if !ok {
		return
	}
	defer func() {
		err := app.ReleaseLock(
			context.Background(),
			app.DbCon,
			lockKey,
		)
		if err != nil {
			hcommon.Log.Warnf("ReleaseLock err: [%T] %s", err, err.Error())
			return
		}
	}()

	txRows, err := app.SQLSelectTTxColByStatus(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTTxID,
			model.DBColTTxProductID,
			model.DBColTTxTxID,
			model.DBColTTxToAddress,
			model.DBColTTxBalanceReal,
		},
		0,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	var productIDs []int64
	for _, txRow := range txRows {
		if !hcommon.IsIntInSlice(productIDs, txRow.ProductID) {
			productIDs = append(productIDs, txRow.ProductID)
		}
	}
	productRows, err := model.SQLSelectTProductCol(
		context.Background(),
		app.DbCon,
		[]string{
			model.DBColTProductID,
			model.DBColTProductAppName,
			model.DBColTProductCbURL,
			model.DBColTProductAppSk,
		},
		productIDs,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	productMap := make(map[int64]*model.DBTProduct)
	for _, productRow := range productRows {
		productMap[productRow.ID] = productRow
	}
	var notifyTxIDs []int64
	var notifyRows []*model.DBTProductNotify
	now := time.Now().Unix()
	for _, txRow := range txRows {
		productRow, ok := productMap[txRow.ProductID]
		if !ok {
			continue
		}
		nonce := hcommon.GetUUIDStr()
		reqObj := gin.H{
			"tx_hash":     txRow.TxID,
			"app_name":    productRow.AppName,
			"address":     txRow.ToAddress,
			"balance":     txRow.BalanceReal,
			"symbol":      "eth",
			"notify_type": 1,
		}
		reqObj["sign"] = hcommon.GetSign(productRow.AppSk, reqObj)
		req, err := json.Marshal(reqObj)
		if err != nil {
			hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
			return
		}
		notifyRows = append(notifyRows, &model.DBTProductNotify{
			Nonce:        nonce,
			ProductID:    txRow.ProductID,
			ItemType:     1,
			ItemID:       txRow.ID,
			NotifyType:   0,
			URL:          productRow.CbURL,
			Msg:          string(req),
			HandleStatus: 0,
			HandleMsg:    "",
			CreateTime:   now,
			UpdateTime:   now,
		})
		notifyTxIDs = append(notifyTxIDs, txRow.ID)
	}
	_, err = model.SQLCreateIgnoreManyTProductNotify(
		context.Background(),
		app.DbCon,
		notifyRows,
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
	_, err = app.SQLUpdateTTxStatusByIDs(
		context.Background(),
		app.DbCon,
		notifyTxIDs,
		model.DBTTx{
			HandleStatus: 1,
			HandleMsg:    "notify",
			HandleTime:   now,
		},
	)
	if err != nil {
		hcommon.Log.Errorf("err: [%T] %s", err, err.Error())
		return
	}
}
