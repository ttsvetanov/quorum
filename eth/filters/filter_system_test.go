// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package filters

import (
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	mux   = new(event.TypeMux)
	db, _ = ethdb.NewMemDatabase()
	api   = NewPublicFilterAPI(db, mux)
)

// TestBlockSubscription tests if a block subscription returns block hashes for posted chain events.
// It creates multiple subscriptions:
// - one at the start and should receive all posted chain events and a second (blockHashes)
// - one that is created after a cutoff moment and uninstalled after a second cutoff moment (blockHashes[cutoff1:cutoff2])
// - one that is created after the second cutoff moment (blockHashes[cutoff2:])
func TestBlockSubscription(t *testing.T) {
	t.Parallel()

	var (
		genesis     = core.WriteGenesisBlockForTesting(db)
		chain, _    = core.GenerateChain(nil, genesis, db, 10, func(i int, gen *core.BlockGen) {})
		chainEvents = []core.ChainEvent{}
	)

	for _, blk := range chain {
		chainEvents = append(chainEvents, core.ChainEvent{Hash: blk.Hash(), Block: blk})
	}

	chan0 := make(chan *types.Header)
	sub0 := api.events.SubscribeNewHeads(chan0)
	chan1 := make(chan *types.Header)
	sub1 := api.events.SubscribeNewHeads(chan1)

	go func() { // simulate client
		i1, i2 := 0, 0
		for i1 != len(chainEvents) || i2 != len(chainEvents) {
			select {
			case header := <-chan0:
				if chainEvents[i1].Hash != header.Hash() {
					t.Errorf("sub0 received invalid hash on index %d, want %x, got %x", i1, chainEvents[i1].Hash, header.Hash())
				}
				i1++
			case header := <-chan1:
				if chainEvents[i2].Hash != header.Hash() {
					t.Errorf("sub1 received invalid hash on index %d, want %x, got %x", i2, chainEvents[i2].Hash, header.Hash())
				}
				i2++
			}
		}

		sub0.Unsubscribe()
		sub1.Unsubscribe()
	}()

	time.Sleep(1 * time.Second)
	for _, e := range chainEvents {
		mux.Post(e)
	}

	<-sub0.Err()
	<-sub1.Err()
}

// TestPendingTxFilter tests whether pending tx filters retrieve all pending transactions that are posted to the event mux.
func TestPendingTxFilter(t *testing.T) {
	t.Parallel()

	var (
		transactions = []*types.Transaction{
			types.NewTransaction(0, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), new(big.Int), new(big.Int), nil),
			types.NewTransaction(1, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), new(big.Int), new(big.Int), nil),
			types.NewTransaction(2, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), new(big.Int), new(big.Int), nil),
			types.NewTransaction(3, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), new(big.Int), new(big.Int), nil),
			types.NewTransaction(4, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), new(big.Int), new(big.Int), nil),
		}

		hashes []common.Hash
	)

	fid0 := api.NewPendingTransactionFilter()

	time.Sleep(1 * time.Second)
	for _, tx := range transactions {
		ev := core.TxPreEvent{Tx: tx}
		mux.Post(ev)
	}

	for {
		h := api.GetFilterChanges(fid0).([]common.Hash)
		hashes = append(hashes, h...)

		if len(hashes) >= len(transactions) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	for i := range hashes {
		if hashes[i] != transactions[i].Hash() {
			t.Errorf("hashes[%d] invalid, want %x, got %x", i, transactions[i].Hash(), hashes[i])
		}
	}
}

// TestLogFilter tests whether log filters match the correct logs that are posted to the event mux.
func TestLogFilter(t *testing.T) {
	t.Parallel()

	var (
		firstAddr      = common.HexToAddress("0x1111111111111111111111111111111111111111")
		secondAddr     = common.HexToAddress("0x2222222222222222222222222222222222222222")
		thirdAddress   = common.HexToAddress("0x3333333333333333333333333333333333333333")
		notUsedAddress = common.HexToAddress("0x9999999999999999999999999999999999999999")
		firstTopic     = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
		secondTopic    = common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
		notUsedTopic   = common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999")

		allLogs = vm.Logs{
			// Note, these are used for comparison of the test cases.
			vm.NewLog(firstAddr, []common.Hash{}, []byte(""), 0),
			vm.NewLog(firstAddr, []common.Hash{firstTopic}, []byte(""), 1),
			vm.NewLog(secondAddr, []common.Hash{firstTopic}, []byte(""), 1),
			vm.NewLog(thirdAddress, []common.Hash{secondTopic}, []byte(""), 2),
			vm.NewLog(thirdAddress, []common.Hash{secondTopic}, []byte(""), 3),
		}

		testCases = []struct {
			crit     FilterCriteria
			expected vm.Logs
			id       rpc.ID
		}{
			// match all
			{FilterCriteria{}, allLogs, ""},
			// match none due to no matching addresses
			{FilterCriteria{Addresses: []common.Address{common.Address{}, notUsedAddress}, Topics: [][]common.Hash{allLogs[0].Topics}}, vm.Logs{}, ""},
			// match logs based on addresses, ignore topics
			{FilterCriteria{Addresses: []common.Address{firstAddr}}, allLogs[:2], ""},
			// match none due to no matching topics (match with address)
			{FilterCriteria{Addresses: []common.Address{secondAddr}, Topics: [][]common.Hash{[]common.Hash{notUsedTopic}}}, vm.Logs{}, ""},
			// match logs based on addresses and topics
			{FilterCriteria{Addresses: []common.Address{thirdAddress}, Topics: [][]common.Hash{[]common.Hash{firstTopic, secondTopic}}}, allLogs[3:5], ""},
			// match logs based on multiple addresses and "or" topics
			{FilterCriteria{Addresses: []common.Address{secondAddr, thirdAddress}, Topics: [][]common.Hash{[]common.Hash{firstTopic, secondTopic}}}, allLogs[2:5], ""},
			// block numbers are ignored for filters created with New***Filter, these return all logs that match the given criterias when the state changes
			{FilterCriteria{Addresses: []common.Address{firstAddr}, FromBlock: big.NewInt(1), ToBlock: big.NewInt(2)}, allLogs[:2], ""},
		}

		err error
	)

	// create all filters
	for i := range testCases {
		testCases[i].id = api.NewFilter(testCases[i].crit)
	}

	// raise events
	time.Sleep(1 * time.Second)
	if err = mux.Post(allLogs); err != nil {
		t.Fatal(err)
	}

	for i, tt := range testCases {
		var fetched []Log
		for { // fetch all expected logs
			fetched = append(fetched, api.GetFilterChanges(tt.id).([]Log)...)
			if len(fetched) >= len(tt.expected) {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if len(fetched) != len(tt.expected) {
			t.Errorf("invalid number of logs for case %d, want %d log(s), got %d", i, len(tt.expected), len(fetched))
			return
		}

		for l := range fetched {
			if fetched[l].Removed {
				t.Errorf("expected log not to be removed for log %d in case %d", l, i)
			}
			if !reflect.DeepEqual(fetched[l].Log, tt.expected[l]) {
				t.Errorf("invalid log on index %d for case %d", l, i)
			}

		}
	}
}

// TestPendingLogsSubscription tests if a subscription receives the correct pending logs that are posted to the event mux.
func TestPendingLogsSubscription(t *testing.T) {
	t.Parallel()

	var (
		firstAddr      = common.HexToAddress("0x1111111111111111111111111111111111111111")
		secondAddr     = common.HexToAddress("0x2222222222222222222222222222222222222222")
		thirdAddress   = common.HexToAddress("0x3333333333333333333333333333333333333333")
		notUsedAddress = common.HexToAddress("0x9999999999999999999999999999999999999999")
		firstTopic     = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
		secondTopic    = common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
		thirdTopic     = common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333")
		forthTopic     = common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444")
		notUsedTopic   = common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999")

		allLogs = []core.PendingLogsEvent{
			core.PendingLogsEvent{Logs: vm.Logs{vm.NewLog(firstAddr, []common.Hash{}, []byte(""), 0)}},
			core.PendingLogsEvent{Logs: vm.Logs{vm.NewLog(firstAddr, []common.Hash{firstTopic}, []byte(""), 1)}},
			core.PendingLogsEvent{Logs: vm.Logs{vm.NewLog(secondAddr, []common.Hash{firstTopic}, []byte(""), 2)}},
			core.PendingLogsEvent{Logs: vm.Logs{vm.NewLog(thirdAddress, []common.Hash{secondTopic}, []byte(""), 3)}},
			core.PendingLogsEvent{Logs: vm.Logs{vm.NewLog(thirdAddress, []common.Hash{secondTopic}, []byte(""), 4)}},
			core.PendingLogsEvent{Logs: vm.Logs{
				vm.NewLog(thirdAddress, []common.Hash{firstTopic}, []byte(""), 5),
				vm.NewLog(thirdAddress, []common.Hash{thirdTopic}, []byte(""), 5),
				vm.NewLog(thirdAddress, []common.Hash{forthTopic}, []byte(""), 5),
				vm.NewLog(firstAddr, []common.Hash{firstTopic}, []byte(""), 5),
			}},
		}

		convertLogs = func(pl []core.PendingLogsEvent) vm.Logs {
			var logs vm.Logs
			for _, l := range pl {
				logs = append(logs, l.Logs...)
			}
			return logs
		}

		testCases = []struct {
			crit     FilterCriteria
			expected vm.Logs
			c        chan []Log
			sub      *Subscription
		}{
			// match all
			{FilterCriteria{}, convertLogs(allLogs), nil, nil},
			// match none due to no matching addresses
			{FilterCriteria{Addresses: []common.Address{common.Address{}, notUsedAddress}, Topics: [][]common.Hash{[]common.Hash{}}}, vm.Logs{}, nil, nil},
			// match logs based on addresses, ignore topics
			{FilterCriteria{Addresses: []common.Address{firstAddr}}, append(convertLogs(allLogs[:2]), allLogs[5].Logs[3]), nil, nil},
			// match none due to no matching topics (match with address)
			{FilterCriteria{Addresses: []common.Address{secondAddr}, Topics: [][]common.Hash{[]common.Hash{notUsedTopic}}}, vm.Logs{}, nil, nil},
			// match logs based on addresses and topics
			{FilterCriteria{Addresses: []common.Address{thirdAddress}, Topics: [][]common.Hash{[]common.Hash{firstTopic, secondTopic}}}, append(convertLogs(allLogs[3:5]), allLogs[5].Logs[0]), nil, nil},
			// match logs based on multiple addresses and "or" topics
			{FilterCriteria{Addresses: []common.Address{secondAddr, thirdAddress}, Topics: [][]common.Hash{[]common.Hash{firstTopic, secondTopic}}}, append(convertLogs(allLogs[2:5]), allLogs[5].Logs[0]), nil, nil},
			// block numbers are ignored for filters created with New***Filter, these return all logs that match the given criterias when the state changes
			{FilterCriteria{Addresses: []common.Address{firstAddr}, FromBlock: big.NewInt(2), ToBlock: big.NewInt(3)}, append(convertLogs(allLogs[:2]), allLogs[5].Logs[3]), nil, nil},
			// multiple pending logs, should match only 2 topics from the logs in block 5
			{FilterCriteria{Addresses: []common.Address{thirdAddress}, Topics: [][]common.Hash{[]common.Hash{firstTopic, forthTopic}}}, vm.Logs{allLogs[5].Logs[0], allLogs[5].Logs[2]}, nil, nil},
		}
	)

	// create all subscriptions, this ensures all subscriptions are created before the events are posted.
	// on slow machines this could otherwise lead to missing events when the subscription is created after
	// (some) events are posted.
	for i := range testCases {
		testCases[i].c = make(chan []Log)
		testCases[i].sub = api.events.SubscribePendingLogs(testCases[i].crit, testCases[i].c)
	}

	for n, test := range testCases {
		i := n
		tt := test
		go func() {
			var fetched []Log
		fetchLoop:
			for {
				logs := <-tt.c
				fetched = append(fetched, logs...)
				if len(fetched) >= len(tt.expected) {
					break fetchLoop
				}
			}

			if len(fetched) != len(tt.expected) {
				t.Fatalf("invalid number of logs for case %d, want %d log(s), got %d", i, len(tt.expected), len(fetched))
			}

			for l := range fetched {
				if fetched[l].Removed {
					t.Errorf("expected log not to be removed for log %d in case %d", l, i)
				}
				if !reflect.DeepEqual(fetched[l].Log, tt.expected[l]) {
					t.Errorf("invalid log on index %d for case %d", l, i)
				}
			}
		}()
	}

	// raise events
	time.Sleep(1 * time.Second)
	for _, l := range allLogs {
		if err := mux.Post(l); err != nil {
			t.Fatal(err)
		}
	}
}
