package eventbus_test

import (
	"context"
	"fmt"
	mrand "math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/internal/eventbus"
	"github.com/tendermint/tendermint/libs/log"
	tmpubsub "github.com/tendermint/tendermint/libs/pubsub"
	tmquery "github.com/tendermint/tendermint/libs/pubsub/query"
	"github.com/tendermint/tendermint/types"
)

func TestEventBusPublishEventTx(t *testing.T) {
	eventBus := eventbus.NewDefault(log.TestingLogger())
	err := eventBus.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			t.Error(err)
		}
	})

	tx := types.Tx("foo")
	result := abci.ResponseDeliverTx{
		Data: []byte("bar"),
		Events: []abci.Event{
			{Type: "testType", Attributes: []abci.EventAttribute{{Key: "baz", Value: "1"}}},
		},
	}

	// PublishEventTx adds 3 composite keys, so the query below should work
	ctx := context.Background()
	query := fmt.Sprintf("tm.event='Tx' AND tx.height=1 AND tx.hash='%X' AND testType.baz=1", tx.Hash())
	txsSub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: "test",
		Query:    tmquery.MustParse(query),
	})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := txsSub.Next(ctx)
		assert.NoError(t, err)

		edt := msg.Data().(types.EventDataTx)
		assert.Equal(t, int64(1), edt.Height)
		assert.Equal(t, uint32(0), edt.Index)
		assert.EqualValues(t, tx, edt.Tx)
		assert.Equal(t, result, edt.Result)
	}()

	err = eventBus.PublishEventTx(types.EventDataTx{
		TxResult: abci.TxResult{
			Height: 1,
			Index:  0,
			Tx:     tx,
			Result: result,
		},
	})
	assert.NoError(t, err)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive a transaction after 1 sec.")
	}
}

func TestEventBusPublishEventNewBlock(t *testing.T) {
	eventBus := eventbus.NewDefault(log.TestingLogger())
	err := eventBus.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			t.Error(err)
		}
	})

	block := types.MakeBlock(0, []types.Tx{}, nil, []types.Evidence{})
	blockID := types.BlockID{Hash: block.Hash(), PartSetHeader: block.MakePartSet(types.BlockPartSizeBytes).Header()}
	resultBeginBlock := abci.ResponseBeginBlock{
		Events: []abci.Event{
			{Type: "testType", Attributes: []abci.EventAttribute{{Key: "baz", Value: "1"}}},
		},
	}
	resultEndBlock := abci.ResponseEndBlock{
		Events: []abci.Event{
			{Type: "testType", Attributes: []abci.EventAttribute{{Key: "foz", Value: "2"}}},
		},
	}

	// PublishEventNewBlock adds the tm.event compositeKey, so the query below should work
	ctx := context.Background()
	query := "tm.event='NewBlock' AND testType.baz=1 AND testType.foz=2"
	blocksSub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: "test",
		Query:    tmquery.MustParse(query),
	})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := blocksSub.Next(ctx)
		assert.NoError(t, err)

		edt := msg.Data().(types.EventDataNewBlock)
		assert.Equal(t, block, edt.Block)
		assert.Equal(t, blockID, edt.BlockID)
		assert.Equal(t, resultBeginBlock, edt.ResultBeginBlock)
		assert.Equal(t, resultEndBlock, edt.ResultEndBlock)
	}()

	err = eventBus.PublishEventNewBlock(types.EventDataNewBlock{
		Block:            block,
		BlockID:          blockID,
		ResultBeginBlock: resultBeginBlock,
		ResultEndBlock:   resultEndBlock,
	})
	assert.NoError(t, err)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive a block after 1 sec.")
	}
}

func TestEventBusPublishEventTxDuplicateKeys(t *testing.T) {
	eventBus := eventbus.NewDefault(log.TestingLogger())
	err := eventBus.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			t.Error(err)
		}
	})

	tx := types.Tx("foo")
	result := abci.ResponseDeliverTx{
		Data: []byte("bar"),
		Events: []abci.Event{
			{
				Type: "transfer",
				Attributes: []abci.EventAttribute{
					{Key: "sender", Value: "foo"},
					{Key: "recipient", Value: "bar"},
					{Key: "amount", Value: "5"},
				},
			},
			{
				Type: "transfer",
				Attributes: []abci.EventAttribute{
					{Key: "sender", Value: "baz"},
					{Key: "recipient", Value: "cat"},
					{Key: "amount", Value: "13"},
				},
			},
			{
				Type: "withdraw.rewards",
				Attributes: []abci.EventAttribute{
					{Key: "address", Value: "bar"},
					{Key: "source", Value: "iceman"},
					{Key: "amount", Value: "33"},
				},
			},
		},
	}

	testCases := []struct {
		query         string
		expectResults bool
	}{
		{
			"tm.event='Tx' AND tx.height=1 AND transfer.sender='DoesNotExist'",
			false,
		},
		{
			"tm.event='Tx' AND tx.height=1 AND transfer.sender='foo'",
			true,
		},
		{
			"tm.event='Tx' AND tx.height=1 AND transfer.sender='baz'",
			true,
		},
		{
			"tm.event='Tx' AND tx.height=1 AND transfer.sender='foo' AND transfer.sender='baz'",
			true,
		},
		{
			"tm.event='Tx' AND tx.height=1 AND transfer.sender='foo' AND transfer.sender='DoesNotExist'",
			false,
		},
	}

	for i, tc := range testCases {
		ctx := context.Background()
		sub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
			ClientID: fmt.Sprintf("client-%d", i),
			Query:    tmquery.MustParse(tc.query),
		})
		require.NoError(t, err)

		gotResult := make(chan bool, 1)
		go func() {
			defer close(gotResult)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			msg, err := sub.Next(ctx)
			if err == nil {
				data := msg.Data().(types.EventDataTx)
				assert.Equal(t, int64(1), data.Height)
				assert.Equal(t, uint32(0), data.Index)
				assert.EqualValues(t, tx, data.Tx)
				assert.Equal(t, result, data.Result)
				gotResult <- true
			}
		}()

		assert.NoError(t, eventBus.PublishEventTx(types.EventDataTx{
			TxResult: abci.TxResult{
				Height: 1,
				Index:  0,
				Tx:     tx,
				Result: result,
			},
		}))

		if got := <-gotResult; got != tc.expectResults {
			require.Failf(t, "Wrong transaction result",
				"got a tx: %v, wanted a tx: %v", got, tc.expectResults)
		}
	}
}

func TestEventBusPublishEventNewBlockHeader(t *testing.T) {
	eventBus := eventbus.NewDefault(log.TestingLogger())
	err := eventBus.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			t.Error(err)
		}
	})

	block := types.MakeBlock(0, []types.Tx{}, nil, []types.Evidence{})
	resultBeginBlock := abci.ResponseBeginBlock{
		Events: []abci.Event{
			{Type: "testType", Attributes: []abci.EventAttribute{{Key: "baz", Value: "1"}}},
		},
	}
	resultEndBlock := abci.ResponseEndBlock{
		Events: []abci.Event{
			{Type: "testType", Attributes: []abci.EventAttribute{{Key: "foz", Value: "2"}}},
		},
	}

	// PublishEventNewBlockHeader adds the tm.event compositeKey, so the query below should work
	ctx := context.Background()
	query := "tm.event='NewBlockHeader' AND testType.baz=1 AND testType.foz=2"
	headersSub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: "test",
		Query:    tmquery.MustParse(query),
	})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := headersSub.Next(ctx)
		assert.NoError(t, err)

		edt := msg.Data().(types.EventDataNewBlockHeader)
		assert.Equal(t, block.Header, edt.Header)
		assert.Equal(t, resultBeginBlock, edt.ResultBeginBlock)
		assert.Equal(t, resultEndBlock, edt.ResultEndBlock)
	}()

	err = eventBus.PublishEventNewBlockHeader(types.EventDataNewBlockHeader{
		Header:           block.Header,
		ResultBeginBlock: resultBeginBlock,
		ResultEndBlock:   resultEndBlock,
	})
	assert.NoError(t, err)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive a block header after 1 sec.")
	}
}

func TestEventBusPublishEventNewEvidence(t *testing.T) {
	eventBus := eventbus.NewDefault(log.TestingLogger())
	err := eventBus.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			t.Error(err)
		}
	})

	ev := types.NewMockDuplicateVoteEvidence(1, time.Now(), "test-chain-id")

	ctx := context.Background()
	const query = `tm.event='NewEvidence'`
	evSub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: "test",
		Query:    tmquery.MustParse(query),
	})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := evSub.Next(ctx)
		assert.NoError(t, err)

		edt := msg.Data().(types.EventDataNewEvidence)
		assert.Equal(t, ev, edt.Evidence)
		assert.Equal(t, int64(4), edt.Height)
	}()

	err = eventBus.PublishEventNewEvidence(types.EventDataNewEvidence{
		Evidence: ev,
		Height:   4,
	})
	assert.NoError(t, err)

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("did not receive a block header after 1 sec.")
	}
}

func TestEventBusPublish(t *testing.T) {
	eventBus := eventbus.NewDefault(log.TestingLogger())
	err := eventBus.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			t.Error(err)
		}
	})

	const numEventsExpected = 14

	ctx := context.Background()
	sub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
		ClientID: "test",
		Query:    tmquery.Empty{},
		Limit:    numEventsExpected,
	})
	require.NoError(t, err)

	count := make(chan int, 1)
	go func() {
		defer close(count)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		for n := 0; ; n++ {
			if _, err := sub.Next(ctx); err != nil {
				count <- n
				return
			}
		}
	}()

	require.NoError(t, eventBus.Publish(types.EventNewBlockHeaderValue,
		types.EventDataNewBlockHeader{}))
	require.NoError(t, eventBus.PublishEventNewBlock(types.EventDataNewBlock{}))
	require.NoError(t, eventBus.PublishEventNewBlockHeader(types.EventDataNewBlockHeader{}))
	require.NoError(t, eventBus.PublishEventVote(types.EventDataVote{}))
	require.NoError(t, eventBus.PublishEventNewRoundStep(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventTimeoutPropose(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventTimeoutWait(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventNewRound(types.EventDataNewRound{}))
	require.NoError(t, eventBus.PublishEventCompleteProposal(types.EventDataCompleteProposal{}))
	require.NoError(t, eventBus.PublishEventPolka(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventUnlock(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventRelock(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventLock(types.EventDataRoundState{}))
	require.NoError(t, eventBus.PublishEventValidatorSetUpdates(types.EventDataValidatorSetUpdates{}))
	require.NoError(t, eventBus.PublishEventBlockSyncStatus(types.EventDataBlockSyncStatus{}))
	require.NoError(t, eventBus.PublishEventStateSyncStatus(types.EventDataStateSyncStatus{}))

	require.GreaterOrEqual(t, <-count, numEventsExpected)
}

func BenchmarkEventBus(b *testing.B) {
	benchmarks := []struct {
		name        string
		numClients  int
		randQueries bool
		randEvents  bool
	}{
		{"10Clients1Query1Event", 10, false, false},
		{"100Clients", 100, false, false},
		{"1000Clients", 1000, false, false},

		{"10ClientsRandQueries1Event", 10, true, false},
		{"100Clients", 100, true, false},
		{"1000Clients", 1000, true, false},

		{"10ClientsRandQueriesRandEvents", 10, true, true},
		{"100Clients", 100, true, true},
		{"1000Clients", 1000, true, true},

		{"10Clients1QueryRandEvents", 10, false, true},
		{"100Clients", 100, false, true},
		{"1000Clients", 1000, false, true},
	}

	for _, bm := range benchmarks {
		bm := bm
		b.Run(bm.name, func(b *testing.B) {
			benchmarkEventBus(bm.numClients, bm.randQueries, bm.randEvents, b)
		})
	}
}

func benchmarkEventBus(numClients int, randQueries bool, randEvents bool, b *testing.B) {
	// for random* functions
	mrand.Seed(time.Now().Unix())

	eventBus := eventbus.NewDefault(log.TestingLogger()) // set buffer capacity to 0 so we are not testing cache
	err := eventBus.Start()
	if err != nil {
		b.Error(err)
	}
	b.Cleanup(func() {
		if err := eventBus.Stop(); err != nil {
			b.Error(err)
		}
	})

	ctx := context.Background()
	q := types.EventQueryNewBlock

	for i := 0; i < numClients; i++ {
		if randQueries {
			q = randQuery()
		}
		sub, err := eventBus.SubscribeWithArgs(ctx, tmpubsub.SubscribeArgs{
			ClientID: fmt.Sprintf("client-%d", i),
			Query:    q,
		})
		if err != nil {
			b.Fatal(err)
		}
		go func() {
			for {
				if _, err := sub.Next(ctx); err != nil {
					return
				}
			}
		}()
	}

	eventValue := types.EventNewBlockValue

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if randEvents {
			eventValue = randEventValue()
		}

		err := eventBus.Publish(eventValue, types.EventDataString("Gamora"))
		if err != nil {
			b.Error(err)
		}
	}
}

var events = []string{
	types.EventNewBlockValue,
	types.EventNewBlockHeaderValue,
	types.EventNewRoundValue,
	types.EventNewRoundStepValue,
	types.EventTimeoutProposeValue,
	types.EventCompleteProposalValue,
	types.EventPolkaValue,
	types.EventUnlockValue,
	types.EventLockValue,
	types.EventRelockValue,
	types.EventTimeoutWaitValue,
	types.EventVoteValue,
	types.EventBlockSyncStatusValue,
	types.EventStateSyncStatusValue,
}

func randEventValue() string {
	return events[mrand.Intn(len(events))]
}

var queries = []tmpubsub.Query{
	types.EventQueryNewBlock,
	types.EventQueryNewBlockHeader,
	types.EventQueryNewRound,
	types.EventQueryNewRoundStep,
	types.EventQueryTimeoutPropose,
	types.EventQueryCompleteProposal,
	types.EventQueryPolka,
	types.EventQueryUnlock,
	types.EventQueryLock,
	types.EventQueryRelock,
	types.EventQueryTimeoutWait,
	types.EventQueryVote,
	types.EventQueryBlockSyncStatus,
	types.EventQueryStateSyncStatus,
}

func randQuery() tmpubsub.Query {
	return queries[mrand.Intn(len(queries))]
}
