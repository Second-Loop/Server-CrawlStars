package rooms

import (
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestNewStoreWithConfigPanicsOnExplicitInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewStoreWithConfig() did not panic for explicit invalid config")
		}
	}()
	NewStoreWithConfig(1, StoreConfig{GameConfig: simulation.GameConfig{Version: 1}})
}
