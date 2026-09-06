package clientconfig

import (
	"io"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func TestClientCombatDisplayMatchesServerCanonicalBudget(t *testing.T) {
	data, err := io.ReadAll(Reader())
	if err != nil {
		t.Fatal(err)
	}
	client, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	server := simulation.StaticGameConfig()
	for _, character := range client.Characters {
		canonical, ok := server.PlayerType(simulation.CharacterType(character.Type))
		if !ok {
			t.Fatalf("missing server character %d", character.Type)
		}
		if character.MaxBullets != canonical.NormalAttack.MaxCharges {
			t.Errorf("character %d client charges=%d server=%d", character.Type, character.MaxBullets, canonical.NormalAttack.MaxCharges)
		}
		if character.SkillAttackCoolDown*server.TickRate != canonical.Skill.CooldownTicks {
			t.Errorf("character %d client skill cooldown=%ds server=%d ticks", character.Type, character.SkillAttackCoolDown, canonical.Skill.CooldownTicks)
		}
	}
}
