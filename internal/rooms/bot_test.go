package rooms

import (
	"math"
	"reflect"
	"testing"

	"github.com/Second-Loop/Server-CrawlStars/internal/simulation"
)

func authoritativeKeyFixture() (
	[]simulation.PlayerData,
	map[string]simulation.InputCommand,
) {
	players := []simulation.PlayerData{
		{ID: "player-a", Team: simulation.TeamRed},
		{ID: "player-b", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 3}, IsBot: true},
		{ID: "player-c", Team: simulation.TeamBlue},
	}
	pending := map[string]simulation.InputCommand{
		"player-c": {PlayerID: "spoof-z", MoveDir: simulation.Vector2{Y: -1}},
		"player-a": {PlayerID: "spoof-a", MoveDir: simulation.Vector2{Y: 1}},
		"player-b": {PlayerID: "player-a", MoveDir: simulation.Vector2{X: -99}},
	}
	return players, pending
}

func TestMergedTickInputsUsesMapKeyAsAuthoritativePlayerID(t *testing.T) {
	players, pending := authoritativeKeyFixture()
	got := mergedTickInputs(pending, players)

	if want := []simulation.PlayerID{"player-a", "player-b", "player-c"}; !reflect.DeepEqual(inputPlayerIDs(got), want) {
		t.Fatalf("merged input IDs = %v, want %v; inputs=%+v", inputPlayerIDs(got), want, got)
	}
	if human := playerInputByID(t, got, "player-a"); human.PlayerID != "player-a" ||
		human.MoveDir != (simulation.Vector2{Y: 1}) {
		t.Fatalf("map key must replace human command value ID: %+v", human)
	}
	bot := playerInputByID(t, got, "player-b")
	if bot.PlayerID != "player-b" || bot.MoveDir == (simulation.Vector2{X: -99}) || !bot.PressedAttack {
		t.Fatalf("bot pending command must be replaced by controller input: %+v", bot)
	}
	if bot.PressedSkill {
		t.Fatalf("server-owned bot must not request skill: %+v", bot)
	}
}

func TestMergedTickInputsPreservesHumanClientTickAndKeepsBotTickZero(t *testing.T) {
	players := []simulation.PlayerData{
		{ID: "human", Team: simulation.TeamRed},
		{ID: "bot", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 3}, IsBot: true},
	}
	pending := map[string]simulation.InputCommand{
		"human": {PlayerID: "spoof", ClientTick: 17, MoveDir: simulation.Vector2{Y: 1}},
		"bot":   {PlayerID: "human", ClientTick: 99, MoveDir: simulation.Vector2{X: -99}},
	}

	got := mergedTickInputs(pending, players)
	human := playerInputByID(t, got, "human")
	if human.ClientTick != 17 || human.MoveDir != (simulation.Vector2{Y: 1}) {
		t.Fatalf("human command lost ClientTick or payload: %+v", human)
	}
	bot := playerInputByID(t, got, "bot")
	if bot.ClientTick != 0 {
		t.Fatalf("server-owned bot ClientTick=%d, want 0", bot.ClientTick)
	}
	if bot.PressedSkill {
		t.Fatalf("server-owned bot must not request skill: %+v", bot)
	}
	if bot.MoveDir == (simulation.Vector2{X: -99}) || !bot.PressedAttack {
		t.Fatalf("bot command must come from controller: %+v", bot)
	}
}

func TestMergedTickInputsDropsIneligibleBotPendingCommands(t *testing.T) {
	tests := []struct {
		name    string
		players []simulation.PlayerData
	}{
		{
			name: "dead bot",
			players: []simulation.PlayerData{
				{ID: "bot", Team: simulation.TeamRed, IsBot: true, IsDead: true},
				{ID: "enemy", Team: simulation.TeamBlue},
			},
		},
		{
			name: "bot without live target",
			players: []simulation.PlayerData{
				{ID: "bot", Team: simulation.TeamRed, IsBot: true},
				{ID: "ally", Team: simulation.TeamRed},
				{ID: "enemy-dead", Team: simulation.TeamBlue, IsDead: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mergedTickInputs(
				map[string]simulation.InputCommand{
					"bot": {PlayerID: "human", MoveDir: simulation.Vector2{X: -99}},
				},
				test.players,
			)
			if len(got) != 0 {
				t.Fatalf("ineligible bot pending command must be dropped, got %+v", got)
			}
		})
	}
}

func TestMergedTickInputsIsDeterministicSortedAndUnique(t *testing.T) {
	players, pending := authoritativeKeyFixture()
	playerPermutations := [][]simulation.PlayerData{
		players,
		{players[2], players[0], players[1]},
		{players[1], players[2], players[0]},
	}
	pendingPermutations := []map[string]simulation.InputCommand{
		pending,
		{
			"player-b": pending["player-b"],
			"player-c": pending["player-c"],
			"player-a": pending["player-a"],
		},
	}

	var want []simulation.InputCommand
	for playerIndex, playerOrder := range playerPermutations {
		for pendingIndex, pendingOrder := range pendingPermutations {
			got := mergedTickInputs(pendingOrder, playerOrder)
			if want == nil {
				want = got
			} else if !reflect.DeepEqual(got, want) {
				t.Fatalf("permutation players=%d pending=%d got %+v, want %+v", playerIndex, pendingIndex, got, want)
			}
			seen := make(map[simulation.PlayerID]struct{}, len(got))
			for _, input := range got {
				if _, duplicate := seen[input.PlayerID]; duplicate {
					t.Fatalf("duplicate PlayerID %q in %+v", input.PlayerID, got)
				}
				seen[input.PlayerID] = struct{}{}
			}
		}
	}
}

func TestBotBasicAttackCadenceLeavesSimulationChargeAuthoritative(t *testing.T) {
	config, err := simulation.StaticGameConfig().SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select duel mode: %v", err)
	}
	playerConfig := config.DefaultPlayerType()
	if playerConfig.NormalAttack.MaxCharges != 3 || playerConfig.NormalAttack.RechargeTicks <= 5 {
		t.Fatalf("unexpected attack fixture: %+v", playerConfig)
	}
	view := []simulation.PlayerData{
		{ID: "bot", Team: simulation.TeamRed, Pos: simulation.Vector2{X: -1}, IsBot: true},
		{ID: "human", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 1}},
	}
	state := simulation.NewStateWithConfig(view, simulation.Config{Game: config})
	nextAttackTicks := make(map[simulation.PlayerID]simulation.Tick)
	accepted := make([]bool, 0, 5)
	for tick := simulation.Tick(1); tick <= 5; tick++ {
		inputs := mergedTickInputsAtTick(nil, botObservation{
			gameMap:         simulation.StaticMapFixture(),
			gameConfig:      config,
			players:         view,
			currentTick:     tick,
			nextAttackTicks: nextAttackTicks,
		}, make(map[simulation.PlayerID]*botControllerState))
		if len(inputs) != 1 || inputs[0].PlayerID != "bot" {
			t.Fatalf("bot input missing or replaced: %+v", inputs)
		}
		if tick == 1 && !inputs[0].PressedAttack {
			t.Fatalf("bot must request its first attack immediately: %+v", inputs)
		}
		if tick > 1 && inputs[0].PressedAttack {
			t.Fatalf("bot must not request attack during cooldown: %+v", inputs)
		}
		snapshot := state.Step(inputs)
		bot := playerByID(t, snapshot.Players, "bot")
		accepted = append(accepted, bot.PressedAttack)
		if bot.PressedAttack {
			nextAttackTicks[bot.ID] = snapshot.Tick + simulation.Tick(playerConfig.NormalAttack.RechargeTicks)
		}
		view = append([]simulation.PlayerData(nil), snapshot.Players...)
	}
	want := []bool{true, false, false, false, false}
	if !reflect.DeepEqual(accepted, want) {
		t.Fatalf("shared simulation accepted=%v, want %v", accepted, want)
	}
}

func TestBotInputForAtTickPreservesMovementAndAimDuringAttackCooldown(t *testing.T) {
	bot := simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, Pos: simulation.Vector2{X: 1, Y: 1}, IsBot: true}
	enemy := simulation.PlayerData{ID: "enemy", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 4, Y: 5}}
	nextAttackTicks := map[simulation.PlayerID]simulation.Tick{bot.ID: 4}

	cooldown, ok := botInputForAtTick(bot, []simulation.PlayerData{bot, enemy}, 3, nextAttackTicks)
	if !ok || cooldown.PressedAttack {
		t.Fatalf("cooldown bot input=%+v ok=%t, want movement/aim without attack", cooldown, ok)
	}
	wantDirection := simulation.Vector2{X: 0.6, Y: 0.8}
	if cooldown.MoveDir != wantDirection || cooldown.AttackDir != wantDirection {
		t.Fatalf("cooldown bot direction move=%+v attack=%+v, want %+v", cooldown.MoveDir, cooldown.AttackDir, wantDirection)
	}

	ready, ok := botInputForAtTick(bot, []simulation.PlayerData{bot, enemy}, 4, nextAttackTicks)
	if !ok || !ready.PressedAttack {
		t.Fatalf("ready bot input=%+v ok=%t, want attack at exact ready tick", ready, ok)
	}
}

func TestRoomBotAttackCadenceUsesServerRechargeTicks(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	config, err := store.gameConfig.SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select duel mode: %v", err)
	}
	config.Map = roomBotIntegrationMap()
	playerType, ok := config.PlayerType(simulation.CharacterTypeShelly)
	if !ok {
		t.Fatal("missing Shelly config")
	}
	config.Player.Types[0].NormalAttack.RechargeTicks = 3
	config.Player.Types[0].NormalAttack.DamagePerHit = 1
	players := []simulation.PlayerData{
		{ID: "bot", Team: simulation.TeamRed, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(2, 4), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed, IsBot: true},
		{ID: "human", Team: simulation.TeamBlue, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(6, 4), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed},
	}
	room := store.newRoomLocked("room-bot-attack-cadence", config)
	room.Status = RoomStatusStarted
	room.matchStatus = MatchStatusStarted
	room.Players = []playerResponse{
		{ID: "bot", Team: string(simulation.TeamRed), IsBot: true, CharacterType: simulation.CharacterTypeShelly},
		{ID: "human", Team: string(simulation.TeamBlue), CharacterType: simulation.CharacterTypeShelly},
	}
	room.lastPlayers = append([]simulation.PlayerData(nil), players...)
	room.state = simulation.NewStateWithConfig(players, simulation.Config{Game: config})

	accepted := make([]bool, 0, 5)
	for range 5 {
		store.tickRoomState(room)
		bot := playerByID(t, room.lastPlayers, "bot")
		accepted = append(accepted, bot.PressedAttack)
		if bot.MoveDir == (simulation.Vector2{}) || bot.AttackDir == (simulation.Vector2{}) {
			t.Fatalf("bot movement/aim stopped during attack cooldown: %+v", bot)
		}
	}
	want := []bool{true, false, false, true, false}
	if !reflect.DeepEqual(accepted, want) {
		t.Fatalf("bot attack approvals=%v, want %v", accepted, want)
	}
}

func TestRoomColtBotRetriesAfterFinalBurstEmission(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	config, err := store.gameConfig.SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select duel mode: %v", err)
	}
	colt, ok := config.PlayerType(simulation.CharacterTypeColt)
	if !ok {
		t.Fatal("missing Colt config")
	}
	config.Map = roomBotIntegrationMap()
	playerType, ok := config.PlayerType(simulation.CharacterTypeColt)
	if !ok {
		t.Fatal("missing Colt player config")
	}
	for index := range config.Player.Types {
		if config.Player.Types[index].CharacterType == simulation.CharacterTypeColt {
			config.Player.Types[index].NormalAttack.DamagePerHit = 1
		}
	}
	players := []simulation.PlayerData{
		{ID: "bot", Team: simulation.TeamRed, CharacterType: simulation.CharacterTypeColt, Pos: config.Map.WorldPos(2, 4), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed, IsBot: true},
		{ID: "human", Team: simulation.TeamBlue, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(6, 4)},
	}
	room := store.newRoomLocked("room-colt-bot-attack-cadence", config)
	room.Status = RoomStatusStarted
	room.matchStatus = MatchStatusStarted
	room.Players = []playerResponse{
		{ID: "bot", Team: string(simulation.TeamRed), IsBot: true, CharacterType: simulation.CharacterTypeColt},
		{ID: "human", Team: string(simulation.TeamBlue), CharacterType: simulation.CharacterTypeShelly},
	}
	room.lastPlayers = append([]simulation.PlayerData(nil), players...)
	room.state = simulation.NewStateWithConfig(players, simulation.Config{Game: config})

	approvedTicks := make([]simulation.Tick, 0, 2)
	lastBurstTick := simulation.Tick(1 + colt.NormalAttack.RechargeTicks)
	for tick := simulation.Tick(1); tick <= lastBurstTick+1; tick++ {
		store.tickRoomState(room)
		if playerByID(t, room.lastPlayers, "bot").PressedAttack {
			approvedTicks = append(approvedTicks, tick)
		}
	}
	want := []simulation.Tick{1, lastBurstTick + 1}
	if !reflect.DeepEqual(approvedTicks, want) {
		t.Fatalf("Colt bot approved ticks=%v, want %v", approvedTicks, want)
	}
}

func TestRoomTickAppliesPlayerCollisionBetweenHumanAndBot(t *testing.T) {
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	config, err := store.gameConfig.SelectMode(simulation.GameModeDuel1v1)
	if err != nil {
		t.Fatalf("select duel mode: %v", err)
	}
	config.Map = roomBotIntegrationMap()
	playerType, ok := config.PlayerType(simulation.CharacterTypeShelly)
	if !ok {
		t.Fatal("missing Shelly config")
	}
	players := []simulation.PlayerData{
		{ID: "human", Team: simulation.TeamRed, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(4, 4), Radius: 0.6, HP: playerType.HP, Speed: playerType.Speed},
		{ID: "bot", Team: simulation.TeamBlue, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(5, 4), Radius: 0.6, HP: playerType.HP, Speed: playerType.Speed, IsBot: true},
	}
	room := store.newRoomLocked("room-player-collision", config)
	room.Status = RoomStatusStarted
	room.matchStatus = MatchStatusStarted
	room.Players = []playerResponse{
		{ID: "human", Team: string(simulation.TeamRed)},
		{ID: "bot", Team: string(simulation.TeamBlue), IsBot: true},
	}
	room.lastPlayers = append([]simulation.PlayerData(nil), players...)
	room.state = simulation.NewStateWithConfig(players, simulation.Config{Game: config})
	room.pendingInputs = map[string]simulation.InputCommand{
		"human": {PlayerID: "human", MoveDir: simulation.Vector2{X: 1}},
	}

	store.tickRoomState(room)

	human := playerByID(t, room.lastPlayers, "human")
	bot := playerByID(t, room.lastPlayers, "bot")
	if human.Pos != players[0].Pos || bot.Pos != players[1].Pos {
		t.Fatalf("human/bot collision positions human=%+v bot=%+v, want %+v/%+v", human.Pos, bot.Pos, players[0].Pos, players[1].Pos)
	}
	if human.MoveDir != (simulation.Vector2{X: 1}) || bot.MoveDir != (simulation.Vector2{X: -1}) {
		t.Fatalf("human/bot inputs were not processed together: human=%+v bot=%+v", human.MoveDir, bot.MoveDir)
	}
}

func TestRoomBotTickUsesOnePreviousObservationAcrossPermutations(t *testing.T) {
	forward := newRoomBotObservationFixture(t, false)
	reversed := newRoomBotObservationFixture(t, true)

	forward.store.tickRoomState(forward.room)
	reversed.store.tickRoomState(reversed.room)
	if len(forward.stepper.inputs) != 1 || len(reversed.stepper.inputs) != 1 {
		t.Fatalf("first tick input batches = %d/%d, want one each", len(forward.stepper.inputs), len(reversed.stepper.inputs))
	}

	forward.room.pendingInputs = map[string]simulation.InputCommand{}
	reversed.room.pendingInputs = map[string]simulation.InputCommand{}
	forward.store.tickRoomState(forward.room)
	reversed.store.tickRoomState(reversed.room)
	if len(forward.stepper.inputs) != 2 || len(reversed.stepper.inputs) != 2 {
		t.Fatalf("second tick input batches = %d/%d, want two each", len(forward.stepper.inputs), len(reversed.stepper.inputs))
	}

	got := forward.stepper.inputs[1]
	if !reflect.DeepEqual(got, reversed.stepper.inputs[1]) {
		t.Fatalf("player/projectile permutation changed second input batch: forward=%+v reversed=%+v", got, reversed.stepper.inputs[1])
	}
	if want := []simulation.PlayerID{"bot-a", "bot-b"}; !reflect.DeepEqual(inputPlayerIDs(got), want) {
		t.Fatalf("second input IDs=%v, want sorted unique IDs %v; inputs=%+v", inputPlayerIDs(got), want, got)
	}
	if bot := playerInputByID(t, got, "bot-a"); bot.MoveDir != (simulation.Vector2{Y: -1}) {
		t.Fatalf("bot-a did not use the previous hostile projectile observation: %+v", bot)
	}
	if human := playerInputByID(t, forward.stepper.inputs[0], "human"); human.PlayerID != "human" ||
		human.ClientTick != 11 || human.MoveDir != (simulation.Vector2{X: -1}) {
		t.Fatalf("human pending command was not authoritative and unique: %+v", human)
	}
	for _, input := range got {
		if input.PressedSkill {
			t.Fatalf("bot batch requested skill: %+v", got)
		}
		if input.PlayerID == "bot-a" || input.PlayerID == "bot-b" {
			if input.ClientTick != 0 {
				t.Fatalf("bot ClientTick=%d, want 0: %+v", input.ClientTick, input)
			}
		}
	}
}

func TestRoomBotControllerAndCadenceStatePersistsThenPrunes(t *testing.T) {
	fixture := newRoomBotLifecycleFixture(t)
	fixture.store.tickRoomState(fixture.room)

	firstStates := roomBotControllerStatePointers(t, fixture.room)
	if _, ok := firstStates["bot-a"]; !ok {
		t.Fatalf("active bot controller state missing after first tick: %+v", firstStates)
	}
	if _, ok := fixture.room.nextBotAttackTicks["stale-bot"]; ok {
		t.Fatalf("stale cadence state survived first generation: %+v", fixture.room.nextBotAttackTicks)
	}

	fixture.room.pendingInputs = map[string]simulation.InputCommand{}
	fixture.store.tickRoomState(fixture.room)
	secondStates := roomBotControllerStatePointers(t, fixture.room)
	if firstStates["bot-a"] != secondStates["bot-a"] {
		t.Fatalf("bot controller state pointer changed across ticks: first=%+v second=%+v", firstStates, secondStates)
	}
	if _, ok := fixture.room.nextBotAttackTicks["bot-a"]; !ok {
		t.Fatalf("active bot cadence state did not persist: %+v", fixture.room.nextBotAttackTicks)
	}

	fixture.room.Players = fixture.room.Players[1:]
	fixture.room.lastPlayers = fixture.room.lastPlayers[1:]
	fixture.room.pendingInputs = map[string]simulation.InputCommand{}
	fixture.store.tickRoomState(fixture.room)
	if got := roomBotControllerStatePointers(t, fixture.room); len(got) != 0 {
		t.Fatalf("controller state for absent participant survived cleanup: %+v", got)
	}
	if _, ok := fixture.room.nextBotAttackTicks["bot-a"]; ok {
		t.Fatalf("cadence state for absent player survived cleanup: %+v", fixture.room.nextBotAttackTicks)
	}
}

func TestRoomBotTickClonesReturnedPlayersAndProjectiles(t *testing.T) {
	fixture := newRoomBotObservationFixture(t, false)
	fixture.store.tickRoomState(fixture.room)

	if len(fixture.room.lastPlayers) == 0 {
		t.Fatal("room did not retain returned players")
	}
	returnedPlayerPosition := fixture.room.lastPlayers[0].Pos
	returnedProjectilePosition := fixture.stepper.snapshots[0].Projectiles[0].Pos
	fixture.stepper.snapshots[0].Players[0].Pos.X = 999
	fixture.stepper.snapshots[0].Projectiles[0].Pos.X = 999
	if fixture.room.lastPlayers[0].Pos != returnedPlayerPosition {
		t.Fatalf("room lastPlayers aliases returned snapshot: got=%+v want=%+v", fixture.room.lastPlayers[0].Pos, returnedPlayerPosition)
	}
	storedProjectiles := roomLastProjectiles(t, fixture.room)
	if len(storedProjectiles) != 1 || storedProjectiles[0].Pos != returnedProjectilePosition {
		t.Fatalf("room lastProjectiles=%+v, want cloned projectile %+v", storedProjectiles, returnedProjectilePosition)
	}
}

func TestRoomBotAttackCadenceUsesSimulationApprovalOnly(t *testing.T) {
	fixture := newRoomBotObservationFixture(t, false)
	for index := range fixture.stepper.snapshots {
		fixture.stepper.snapshots[index].Players[0].PressedAttack = false
	}

	fixture.store.tickRoomState(fixture.room)
	fixture.room.pendingInputs = map[string]simulation.InputCommand{}
	fixture.store.tickRoomState(fixture.room)
	if len(fixture.stepper.inputs) != 2 {
		t.Fatalf("recorded input batches=%d, want 2", len(fixture.stepper.inputs))
	}
	for index, inputs := range fixture.stepper.inputs {
		if bot := playerInputByID(t, inputs, "bot-a"); !bot.PressedAttack {
			t.Fatalf("tick %d bot attack request=%+v, want retry because simulation approved no attack", index+1, bot)
		}
	}
}

func TestRoomBotCleanupDropsRoomRegistryOwnership(t *testing.T) {
	fixture := newRoomBotObservationFixture(t, false)
	fixture.store.mu.Lock()
	fixture.store.rooms[fixture.room.ID] = fixture.room
	fixture.store.mu.Unlock()
	fixture.store.tickRoomState(fixture.room)

	var resources roomResources
	fixture.room.mu.Lock()
	_, removed := resources.removeRoomLockedWithCause(fixture.room, websocketCloseCauseDebugDelete)
	fixture.room.mu.Unlock()
	if !removed {
		t.Fatal("room cleanup did not remove room-owned resources")
	}
	if !fixture.store.deleteRoomIfSame(fixture.room.ID, fixture.room) {
		t.Fatal("room cleanup did not release registry ownership")
	}
	if fixture.store.lookupRoom(fixture.room.ID) != nil {
		t.Fatal("cleaned room remained reachable from the store")
	}
}

type roomBotTickStepper struct {
	inputs    [][]simulation.InputCommand
	snapshots []simulation.Snapshot
}

func (s *roomBotTickStepper) EliminatePlayers([]simulation.PlayerID) {}

func (s *roomBotTickStepper) Step(inputs []simulation.InputCommand) simulation.Snapshot {
	s.inputs = append(s.inputs, append([]simulation.InputCommand(nil), inputs...))
	index := len(s.inputs) - 1
	if index >= len(s.snapshots) {
		index = len(s.snapshots) - 1
	}
	if index < 0 {
		return simulation.Snapshot{}
	}
	return s.snapshots[index]
}

type roomBotObservationFixture struct {
	store   *Store
	room    *room
	stepper *roomBotTickStepper
}

func newRoomBotObservationFixture(t *testing.T, reverse bool) roomBotObservationFixture {
	t.Helper()
	store := NewStoreWithClock(5, newFakeClock())
	t.Cleanup(store.Close)
	config, err := store.gameConfig.SelectMode(simulation.GameModeSolo)
	if err != nil {
		t.Fatalf("select solo mode: %v", err)
	}
	config.Map = roomBotIntegrationMap()
	playerType, ok := config.PlayerType(simulation.CharacterTypeShelly)
	if !ok {
		t.Fatal("missing Shelly config")
	}
	players := []simulation.PlayerData{
		{ID: "bot-a", Team: simulation.TeamRed, IsBot: true, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(4, 4), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed},
		{ID: "bot-b", Team: simulation.TeamRed, IsBot: true, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(4, 5), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed},
		{ID: "enemy", Team: simulation.TeamBlue, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(6, 4), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed},
		{ID: "human", Team: simulation.TeamBlue, CharacterType: simulation.CharacterTypeShelly, Pos: config.Map.WorldPos(6, 5), Radius: playerType.Radius, HP: playerType.HP, Speed: playerType.Speed},
	}
	projectiles := []simulation.ProjectileData{{
		ID:      "threat-a",
		OwnerID: "enemy",
		Pos:     simulation.Vector2{X: -2, Y: 0.2},
		Dir:     simulation.Vector2{X: 1},
		Radius:  0.1,
	}}
	firstPlayers := append([]simulation.PlayerData(nil), players...)
	if reverse {
		reversePlayerData(firstPlayers)
	}
	secondPlayers := append([]simulation.PlayerData(nil), firstPlayers...)
	firstSnapshot := simulation.Snapshot{Tick: 1, Players: firstPlayers, Projectiles: projectiles}
	secondSnapshot := simulation.Snapshot{Tick: 2, Players: secondPlayers, Projectiles: append([]simulation.ProjectileData(nil), projectiles...)}
	stepper := &roomBotTickStepper{snapshots: []simulation.Snapshot{firstSnapshot, secondSnapshot}}
	room := store.newRoomLocked("room-bot-observation", config)
	room.Status = RoomStatusStarted
	room.matchStatus = MatchStatusStarted
	room.Players = roomBotPlayerResponses(players)
	room.lastPlayers = append([]simulation.PlayerData(nil), firstPlayers...)
	room.pendingInputs = map[string]simulation.InputCommand{
		"human": {PlayerID: "spoof", ClientTick: 11, MoveDir: simulation.Vector2{X: -1}},
		"bot-a": {PlayerID: "human", MoveDir: simulation.Vector2{X: 99}},
	}
	room.state = stepper
	return roomBotObservationFixture{store: store, room: room, stepper: stepper}
}

func newRoomBotLifecycleFixture(t *testing.T) roomBotObservationFixture {
	fixture := newRoomBotObservationFixture(t, false)
	fixture.room.Players = fixture.room.Players[:2]
	fixture.room.Players[1].ID = "enemy"
	fixture.room.lastPlayers = fixture.room.lastPlayers[:2]
	fixture.room.lastPlayers[1].ID = "enemy"
	fixture.room.lastPlayers[1].IsBot = false
	fixture.room.nextBotAttackTicks["stale-bot"] = 99
	fixture.stepper.snapshots = []simulation.Snapshot{
		{Tick: 1, Players: append([]simulation.PlayerData(nil), fixture.room.lastPlayers...), Projectiles: nil},
		{Tick: 2, Players: append([]simulation.PlayerData(nil), fixture.room.lastPlayers...), Projectiles: nil},
		{Tick: 3, Players: []simulation.PlayerData{fixture.room.lastPlayers[1]}, Projectiles: nil},
	}
	fixture.stepper.snapshots[0].Players[0].PressedAttack = true
	fixture.stepper.snapshots[1].Players[0].PressedAttack = false
	return fixture
}

func roomBotIntegrationMap() simulation.MapData {
	const width = 9
	const height = 9
	grid := make([][]simulation.TileType, height)
	for y := range grid {
		grid[y] = make([]simulation.TileType, width)
		for x := range grid[y] {
			if x == 0 || y == 0 || x == width-1 || y == height-1 {
				grid[y][x] = simulation.TileWall
			} else {
				grid[y][x] = simulation.TileGround
			}
		}
	}
	return simulation.MapData{Width: width, Height: height, MaxPlayers: 6, TileSize: simulation.TileSize, Map: grid}
}

func roomBotPlayerResponses(players []simulation.PlayerData) []playerResponse {
	responses := make([]playerResponse, 0, len(players))
	for index, player := range players {
		responses = append(responses, playerResponse{
			ID:            string(player.ID),
			Team:          string(player.Team),
			Slot:          index,
			IsBot:         player.IsBot,
			CharacterType: player.CharacterType,
		})
	}
	return responses
}

func reversePlayerData(players []simulation.PlayerData) {
	for left, right := 0, len(players)-1; left < right; left, right = left+1, right-1 {
		players[left], players[right] = players[right], players[left]
	}
}

func roomBotControllerStatePointers(t *testing.T, room *room) map[simulation.PlayerID]uintptr {
	t.Helper()
	field := reflect.ValueOf(room).Elem().FieldByName("botControllerStates")
	if !field.IsValid() {
		t.Fatalf("room is missing botControllerStates")
	}
	if field.Kind() != reflect.Map {
		t.Fatalf("botControllerStates kind=%s, want map", field.Kind())
	}
	got := make(map[simulation.PlayerID]uintptr, field.Len())
	for _, key := range field.MapKeys() {
		value := field.MapIndex(key)
		if value.IsNil() {
			got[simulation.PlayerID(key.String())] = 0
			continue
		}
		got[simulation.PlayerID(key.String())] = value.Pointer()
	}
	return got
}

func roomLastProjectiles(t *testing.T, room *room) []simulation.ProjectileData {
	t.Helper()
	return append([]simulation.ProjectileData(nil), room.lastProjectiles...)
}

func inputPlayerIDs(inputs []simulation.InputCommand) []simulation.PlayerID {
	ids := make([]simulation.PlayerID, len(inputs))
	for index, input := range inputs {
		ids[index] = input.PlayerID
	}
	return ids
}

func playerInputByID(
	t *testing.T,
	inputs []simulation.InputCommand,
	id simulation.PlayerID,
) simulation.InputCommand {
	t.Helper()
	for _, input := range inputs {
		if input.PlayerID == id {
			return input
		}
	}
	t.Fatalf("missing input %q in %+v", id, inputs)
	return simulation.InputCommand{}
}

func playerByID(
	t *testing.T,
	players []simulation.PlayerData,
	id simulation.PlayerID,
) simulation.PlayerData {
	t.Helper()
	for _, player := range players {
		if player.ID == id {
			return player
		}
	}
	t.Fatalf("missing player %q in %+v", id, players)
	return simulation.PlayerData{}
}

func TestNearestLiveEnemy(t *testing.T) {
	bot := simulation.PlayerData{
		ID:    "bot",
		Team:  simulation.TeamRed,
		Pos:   simulation.Vector2{X: 1, Y: 1},
		IsBot: true,
	}
	nearEnemy := simulation.PlayerData{
		ID:   "enemy-near",
		Team: simulation.TeamBlue,
		Pos:  simulation.Vector2{X: 4, Y: 5},
	}

	tests := []struct {
		name    string
		players []simulation.PlayerData
		wantID  simulation.PlayerID
		wantOK  bool
	}{
		{
			name: "self is excluded",
			players: []simulation.PlayerData{
				{ID: bot.ID, Team: simulation.TeamBlue, Pos: bot.Pos},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "ally is excluded",
			players: []simulation.PlayerData{
				bot,
				{ID: "ally", Team: simulation.TeamRed, Pos: simulation.Vector2{X: 1.1, Y: 1}},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "dead enemy is excluded",
			players: []simulation.PlayerData{
				bot,
				{ID: "enemy-dead", Team: simulation.TeamBlue, Pos: bot.Pos, IsDead: true},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "smallest squared distance wins",
			players: []simulation.PlayerData{
				bot,
				{ID: "enemy-far", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 7, Y: 1}},
				nearEnemy,
			},
			wantID: nearEnemy.ID,
			wantOK: true,
		},
		{
			name: "equal distance uses lexical ID when lexical winner is later",
			players: []simulation.PlayerData{
				bot,
				{ID: "enemy-z", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 2, Y: 1}},
				{ID: "enemy-a", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 0, Y: 1}},
			},
			wantID: "enemy-a",
			wantOK: true,
		},
		{
			name: "equal distance uses lexical ID when lexical winner is earlier",
			players: []simulation.PlayerData{
				{ID: "enemy-a", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 0, Y: 1}},
				bot,
				{ID: "enemy-z", Team: simulation.TeamBlue, Pos: simulation.Vector2{X: 2, Y: 1}},
			},
			wantID: "enemy-a",
			wantOK: true,
		},
		{
			name: "no live enemy",
			players: []simulation.PlayerData{
				bot,
				{ID: "ally", Team: simulation.TeamRed},
				{ID: "enemy-dead", Team: simulation.TeamBlue, IsDead: true},
			},
			wantOK: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := nearestLiveEnemy(bot, test.players)
			if ok != test.wantOK {
				t.Fatalf("nearestLiveEnemy() ok = %t, want %t; player=%+v", ok, test.wantOK, got)
			}
			if ok && got.ID != test.wantID {
				t.Fatalf("nearestLiveEnemy() ID = %q, want %q; player=%+v", got.ID, test.wantID, got)
			}
		})
	}
}

func TestBotInputForUsesUnitMovementAimAndBasicAttack(t *testing.T) {
	bot := simulation.PlayerData{
		ID: "bot", Team: simulation.TeamRed,
		Pos: simulation.Vector2{X: 1, Y: 1}, IsBot: true,
	}
	enemy := simulation.PlayerData{
		ID: "enemy", Team: simulation.TeamBlue,
		Pos: simulation.Vector2{X: 4, Y: 5},
	}
	got, ok := botInputFor(bot, []simulation.PlayerData{bot, enemy})
	if !ok {
		t.Fatal("expected bot input")
	}
	want := simulation.Vector2{X: 0.6, Y: 0.8}
	if got.PlayerID != bot.ID || got.MoveDir != want || got.AttackDir != want ||
		!got.PressedAttack {
		t.Fatalf("unexpected bot input: %+v", got)
	}
	if delta := math.Abs(math.Hypot(got.MoveDir.X, got.MoveDir.Y) - 1); delta > 1e-12 {
		t.Fatalf("MoveDir is not unit length: %+v", got.MoveDir)
	}
}

func TestBotInputForCoincidentEnemyUsesApprovedPositiveX(t *testing.T) {
	bot := simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, IsBot: true}
	enemy := simulation.PlayerData{ID: "enemy", Team: simulation.TeamBlue}
	got, ok := botInputFor(bot, []simulation.PlayerData{bot, enemy})
	want := simulation.Vector2{X: 1, Y: 0}
	if !ok || got.PlayerID != bot.ID || got.MoveDir != want || got.AttackDir != want ||
		!got.PressedAttack {
		t.Fatalf("expected approved +X fallback, got %+v ok=%t", got, ok)
	}
}

func TestBotInputForRejectsIneligibleCallerOrMissingTarget(t *testing.T) {
	tests := []struct {
		name    string
		bot     simulation.PlayerData
		players []simulation.PlayerData
	}{
		{
			name: "non-bot caller",
			bot:  simulation.PlayerData{ID: "human", Team: simulation.TeamRed},
			players: []simulation.PlayerData{
				{ID: "enemy", Team: simulation.TeamBlue},
			},
		},
		{
			name: "dead bot",
			bot:  simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, IsBot: true, IsDead: true},
			players: []simulation.PlayerData{
				{ID: "enemy", Team: simulation.TeamBlue},
			},
		},
		{
			name: "no live enemy",
			bot:  simulation.PlayerData{ID: "bot", Team: simulation.TeamRed, IsBot: true},
			players: []simulation.PlayerData{
				{ID: "bot", Team: simulation.TeamRed, IsBot: true},
				{ID: "ally", Team: simulation.TeamRed},
				{ID: "enemy-dead", Team: simulation.TeamBlue, IsDead: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := botInputFor(test.bot, test.players)
			if ok {
				t.Fatalf("botInputFor() = %+v, true; want false", got)
			}
			if got != (simulation.InputCommand{}) {
				t.Fatalf("botInputFor() rejected command = %+v, want zero value", got)
			}
		})
	}
}

func TestBotInputDeterministicAcrossPlayerOrder(t *testing.T) {
	bot := simulation.PlayerData{
		ID: "bot", Team: simulation.TeamRed, IsBot: true,
	}
	enemyA := simulation.PlayerData{
		ID: "enemy-a", Team: simulation.TeamBlue,
		Pos: simulation.Vector2{X: -1},
	}
	enemyZ := simulation.PlayerData{
		ID: "enemy-z", Team: simulation.TeamBlue,
		Pos: simulation.Vector2{X: 1},
	}
	permutations := [][]simulation.PlayerData{
		{bot, enemyA, enemyZ},
		{bot, enemyZ, enemyA},
		{enemyA, bot, enemyZ},
		{enemyA, enemyZ, bot},
		{enemyZ, bot, enemyA},
		{enemyZ, enemyA, bot},
	}

	var want simulation.InputCommand
	for index, players := range permutations {
		got, ok := botInputFor(bot, players)
		if !ok {
			t.Fatalf("permutation %d produced no input", index)
		}
		if index == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("permutation %d got %+v, want %+v", index, got, want)
		}
	}
	if want.AttackDir != (simulation.Vector2{X: -1}) {
		t.Fatalf("equal-distance tie must choose enemy-a, got %+v", want)
	}
}
