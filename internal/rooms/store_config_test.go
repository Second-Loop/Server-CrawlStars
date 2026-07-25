package rooms

import (
	"reflect"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestNewStoreWithConfigFallsBackFromVersionOneToStaticV2Config(t *testing.T) {
	store := NewStoreWithConfig(1, StoreConfig{
		GameConfig: simulation.GameConfig{Version: 1},
	})
	t.Cleanup(store.Close)

	want := simulation.StaticGameConfig()
	if store.gameConfig.Version != simulation.GameConfigVersion {
		t.Fatalf("store config version = %d, want %d", store.gameConfig.Version, simulation.GameConfigVersion)
	}
	if !reflect.DeepEqual(store.gameConfig.Player.Types, want.Player.Types) {
		t.Fatalf("store character catalog = %+v, want %+v", store.gameConfig.Player.Types, want.Player.Types)
	}
	if !reflect.DeepEqual(store.gameMap, want.Map) {
		t.Fatalf("store map = %+v, want static map %+v", store.gameMap, want.Map)
	}
}
