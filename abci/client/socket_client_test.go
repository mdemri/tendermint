package abciclient_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"math/rand"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	abciclient "github.com/tendermint/tendermint/abci/client"
	"github.com/tendermint/tendermint/abci/server"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/libs/service"
)

var ctx = context.Background()

func TestProperSyncCalls(t *testing.T) {
	app := slowApp{}
	logger := log.TestingLogger()

	s, c := setupClientServer(t, logger, app)
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if err := c.Stop(); err != nil {
			t.Error(err)
		}
	})

	resp := make(chan error, 1)
	go func() {
		// This is BeginBlockSync unrolled....
		reqres, err := c.BeginBlockAsync(ctx, types.RequestBeginBlock{})
		assert.NoError(t, err)
		err = c.FlushSync(context.Background())
		assert.NoError(t, err)
		res := reqres.Response.GetBeginBlock()
		assert.NotNil(t, res)
		resp <- c.Error()
	}()

	select {
	case <-time.After(time.Second):
		require.Fail(t, "No response arrived")
	case err, ok := <-resp:
		require.True(t, ok, "Must not close channel")
		assert.NoError(t, err, "This should return success")
	}
}

func TestHangingSyncCalls(t *testing.T) {
	app := slowApp{}
	logger := log.TestingLogger()

	s, c := setupClientServer(t, logger, app)
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Log(err)
		}
	})
	t.Cleanup(func() {
		if err := c.Stop(); err != nil {
			t.Log(err)
		}
	})

	resp := make(chan error, 1)
	go func() {
		// Start BeginBlock and flush it
		reqres, err := c.BeginBlockAsync(ctx, types.RequestBeginBlock{})
		assert.NoError(t, err)
		flush, err := c.FlushAsync(ctx)
		assert.NoError(t, err)
		// wait 20 ms for all events to travel socket, but
		// no response yet from server
		time.Sleep(20 * time.Millisecond)
		// kill the server, so the connections break
		err = s.Stop()
		assert.NoError(t, err)

		// wait for the response from BeginBlock
		reqres.Wait()
		flush.Wait()
		resp <- c.Error()
	}()

	select {
	case <-time.After(time.Second):
		require.Fail(t, "No response arrived")
	case err, ok := <-resp:
		require.True(t, ok, "Must not close channel")
		assert.Error(t, err, "We should get EOF error")
	}
}

func setupClientServer(
	t *testing.T,
	logger log.Logger,
	app types.Application,
) (service.Service, abciclient.Client) {
	t.Helper()

	// some port between 20k and 30k
	port := 20000 + rand.Int31()%10000
	addr := fmt.Sprintf("localhost:%d", port)

	s, err := server.NewServer(logger, addr, "socket", app)
	require.NoError(t, err)
	err = s.Start()
	require.NoError(t, err)

	c := abciclient.NewSocketClient(logger, addr, true)
	err = c.Start()
	require.NoError(t, err)

	return s, c
}

type slowApp struct {
	types.BaseApplication
}

func (slowApp) BeginBlock(req types.RequestBeginBlock) types.ResponseBeginBlock {
	time.Sleep(200 * time.Millisecond)
	return types.ResponseBeginBlock{}
}
