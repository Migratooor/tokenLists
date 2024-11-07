package main

import (
	"context"
	"strconv"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/migratooor/tokenLists/generators/common/chains"
	"github.com/migratooor/tokenLists/generators/common/contracts"
	"github.com/migratooor/tokenLists/generators/common/ethereum"
	"github.com/migratooor/tokenLists/generators/common/helpers"
	"github.com/migratooor/tokenLists/generators/common/logs"
	"github.com/migratooor/tokenLists/generators/common/models"
	"github.com/migratooor/tokenLists/generators/common/utils"
)

func handleUniswapPairsTokenList(tokensPerChainID map[uint64][]string) []models.TokenListToken {
	tokensForChainIDSyncMap := helpers.InitSyncMap(tokensPerChainID)

	// Fetch the basic informations for all the tokens for all the chains
	perChainWG := sync.WaitGroup{}
	perChainWG.Add(len(tokensPerChainID))
	for chainID, list := range tokensPerChainID {
		go func(chainID uint64, list []string) {
			defer perChainWG.Done()
			syncMapRaw, _ := tokensForChainIDSyncMap.Load(chainID)
			syncMap := syncMapRaw.([]models.TokenListToken)

			tokensInfo := helpers.RetrieveBasicInformations(chainID, list)
			for _, address := range list {
				if token, ok := tokensInfo[utils.ToAddress(address)]; ok {
					if token.Name == `` || token.Symbol == `` {
						continue
					}

					if newToken, err := helpers.SetToken(
						token.Address,
						token.Name,
						token.Symbol,
						``,
						chainID,
						int(token.Decimals),
					); err == nil {
						syncMap = append(syncMap, newToken)
						tokensForChainIDSyncMap.Store(chainID, syncMap)
					}
				}
			}
		}(chainID, list)
	}
	perChainWG.Wait()

	return helpers.ExtractSyncMap(tokensForChainIDSyncMap)
}

func fetchUniswapPairsTokenList(extra map[string]interface{}) ([]models.TokenListToken, map[uint64]string) {
	tokensPerChainID := make(map[uint64][]string)
	allTokens := make(map[string]int)
	lastBlockSync := make(map[uint64]string)

	/**************************************************************************
	** Looping through all the Uniswap contracts per chainID to read the logs
	** and see the pairs and tokens that are being used.
	** In order to be included, a PAIR must have tokens that are both in at
	** least 10 different pairs.
	**************************************************************************/
	for chainID, uniContract := range UniswapContractsPerChainID {
		if !chains.IsChainIDSupported(chainID) {
			continue
		}
		tokensPerChainID[chainID] = []string{}

		/**********************************************************************
		** Init the RPC and get the current block number to know where to start
		** and end the logs fetching
		**********************************************************************/
		chainIDStr := strconv.FormatUint(chainID, 10)
		lastBlockSyncForChainID := uint64(0)
		if sync, ok := extra[`lastBlockSyncFor_`+chainIDStr]; ok {
			lastBlockSyncForChainID, _ = strconv.ParseUint(sync.(string), 10, 64)
		}
		client := ethereum.GetRPC(chainID).(*ethclient.Client)
		currentBlockNumber, _ := client.BlockNumber(context.Background())
		threshold := uint64(100_000)
		if chainID == 56 {
			threshold = uint64(5_000)
		}

		for _, uniContract := range uniContract {
			start := uniContract.BlockNumber.Uint64()
			if (lastBlockSyncForChainID != 0) && (lastBlockSyncForChainID > start) {
				start = lastBlockSyncForChainID
			}
			if uniContract.Type == 2 {
				uniV2Factory, _ := contracts.NewUniV2Factory(uniContract.ContractAddress, client)
				for startBlockToTest := start; startBlockToTest <= currentBlockNumber; startBlockToTest += threshold {
					end := startBlockToTest + threshold
					if end > currentBlockNumber {
						end = currentBlockNumber
					}
					options := &bind.FilterOpts{
						Start:   startBlockToTest,
						End:     &end,
						Context: nil,
					}
					logs.Info(`v2 - start: `, startBlockToTest, ` end: `, end, ` total: `, len(allTokens), ` current block: `, currentBlockNumber, ` chainID: `, chainIDStr)
					if log, err := uniV2Factory.FilterPairCreated(options, nil, nil); err == nil {
						for log.Next() {
							if log.Error() != nil {
								continue
							}
							if _, ok := allTokens[log.Event.Token0.Hex()]; !ok {
								allTokens[log.Event.Token0.Hex()] = 0
							}
							if _, ok := allTokens[log.Event.Token1.Hex()]; !ok {
								allTokens[log.Event.Token1.Hex()] = 0
							}
							allTokens[log.Event.Token0.Hex()]++
							allTokens[log.Event.Token1.Hex()]++
						}
					} else {
						logs.Error("Error fetching all tokens from uniswap factory contract: ", err)
					}
				}
			} else if uniContract.Type == 3 {
				uniV3Factory, _ := contracts.NewUniV3Factory(uniContract.ContractAddress, client)
				for startBlockToTest := start; startBlockToTest <= currentBlockNumber; startBlockToTest += threshold {
					end := startBlockToTest + threshold
					if end > currentBlockNumber {
						end = currentBlockNumber
					}
					options := &bind.FilterOpts{
						Start:   startBlockToTest,
						End:     &end,
						Context: nil,
					}
					logs.Info(`v3 - start: `, startBlockToTest, ` end: `, end, ` total: `, len(allTokens), ` current block: `, currentBlockNumber, ` chainID: `, chainIDStr)
					if log, err := uniV3Factory.FilterPoolCreated(options, nil, nil, nil); err == nil {
						for log.Next() {
							if log.Error() != nil {
								continue
							}
							if _, ok := allTokens[log.Event.Token0.Hex()]; !ok {
								allTokens[log.Event.Token0.Hex()] = 0
							}
							if _, ok := allTokens[log.Event.Token1.Hex()]; !ok {
								allTokens[log.Event.Token1.Hex()] = 0
							}
							allTokens[log.Event.Token0.Hex()]++
							allTokens[log.Event.Token1.Hex()]++
						}
					} else {
						logs.Error("Error fetching all tokens from uniswap factory contract: ", err)
					}
				}
			}
			lastBlockSync[chainID] = strconv.FormatUint(currentBlockNumber, 10)
		}

		/**********************************************************************
		** Transforming the output to the format that we need for the handle
		** function
		**********************************************************************/
		for address, count := range allTokens {
			if chains.IsTokenIgnored(chainID, address) {
				continue
			}
			if count >= UNI_POOL_THRESHOLD_FOR_CHAINID[chainID] {
				tokensPerChainID[chainID] = append(tokensPerChainID[chainID], address)
			}
		}
	}

	return handleUniswapPairsTokenList(tokensPerChainID), lastBlockSync
}

func buildUniswapPairsTokenList() {
	tokenList := helpers.LoadTokenListFromJsonFile(`uniswap-pairs.json`)
	tokenList.Name = "Uniswap Token Pairs"
	tokenList.LogoURI = "ipfs://QmNa8mQkrNKp1WEEeGjFezDmDeodkWRevGFN8JCV7b4Xir"

	tokens, lastBlockSync := fetchUniswapPairsTokenList(tokenList.Metadata)
	if tokenList.Metadata == nil {
		tokenList.Metadata = make(map[string]interface{})
	}
	for chainID, blockNumber := range lastBlockSync {
		chainIDStr := strconv.FormatUint(chainID, 10)
		tokenList.Metadata[`lastBlockSyncFor_`+chainIDStr] = blockNumber
	}

	helpers.SaveTokenListInJsonFile(tokenList, tokens, `uniswap-pairs.json`, helpers.SavingMethodAppend)
}
