package authentication

import (
	"testing"
	"time"

	hplugin "github.com/hashicorp/go-plugin"
)

func TestRuntimeCloseDoesNotWaitForPluginRequestSerialization(t *testing.T) {
	process := &hplugin.Client{}
	client := &pluginClient{client: process}
	client.mu.Lock()

	closed := make(chan struct{})
	go func() {
		(&Runtime{pluginProcesses: []*hplugin.Client{process}}).Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(time.Second):
		client.mu.Unlock()
		t.Fatal("Runtime.Close waited for the request serialization mutex")
	}
	client.mu.Unlock()
}
