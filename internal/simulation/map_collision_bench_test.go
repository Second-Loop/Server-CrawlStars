package simulation

import "testing"

func BenchmarkCollidesWithMapOptimized(b *testing.B) {
	gameMap := benchmarkCollisionMap(128, 128)
	state := &State{gameMap: gameMap}
	position := gameMap.WorldPos(64, 64)

	b.ReportAllocs()
	b.ResetTimer()
	var got bool
	for i := 0; i < b.N; i++ {
		got = state.collidesWithMap(position, DefaultPlayerRadius, tileBlocksPlayer)
	}
	b.StopTimer()
	if got {
		b.Fatal("benchmark query unexpectedly collided")
	}
}

func BenchmarkCollidesWithMapExhaustive(b *testing.B) {
	gameMap := benchmarkCollisionMap(128, 128)
	position := gameMap.WorldPos(64, 64)

	b.ReportAllocs()
	b.ResetTimer()
	var got bool
	for i := 0; i < b.N; i++ {
		got = exhaustiveCollidesWithMap(gameMap, position, DefaultPlayerRadius, tileBlocksPlayer)
	}
	b.StopTimer()
	if got {
		b.Fatal("benchmark query unexpectedly collided")
	}
}

func BenchmarkFirstBlockingSegmentTOptimized(b *testing.B) {
	gameMap := benchmarkCollisionMap(128, 128)
	state := &State{gameMap: gameMap}
	start := gameMap.WorldPos(48, 48)
	end := gameMap.WorldPos(80, 80)

	b.ReportAllocs()
	b.ResetTimer()
	var got float64
	for i := 0; i < b.N; i++ {
		got = state.firstBlockingSegmentT(start, end)
	}
	b.StopTimer()
	if got < 0 {
		b.Fatalf("benchmark returned invalid blocking t %v", got)
	}
}

func BenchmarkFirstBlockingSegmentTExhaustive(b *testing.B) {
	gameMap := benchmarkCollisionMap(128, 128)
	start := gameMap.WorldPos(48, 48)
	end := gameMap.WorldPos(80, 80)

	b.ReportAllocs()
	b.ResetTimer()
	var got float64
	for i := 0; i < b.N; i++ {
		got = exhaustiveFirstBlockingSegmentT(gameMap, start, end)
	}
	b.StopTimer()
	if got < 0 {
		b.Fatalf("benchmark returned invalid blocking t %v", got)
	}
}

func BenchmarkStateStepWithLargeMap(b *testing.B) {
	gameMap := benchmarkCollisionMap(128, 128)
	config := StaticGameConfig()
	config.Map = gameMap
	players := []PlayerData{
		{ID: "red-1", Team: TeamRed, Pos: gameMap.WorldPos(48, 48)},
		{ID: "blue-1", Team: TeamBlue, Pos: gameMap.WorldPos(80, 80)},
		{ID: "red-2", Team: TeamRed, Pos: gameMap.WorldPos(48, 80)},
		{ID: "blue-2", Team: TeamBlue, Pos: gameMap.WorldPos(80, 48)},
	}
	state := NewStateWithConfig(players, Config{Game: config})
	inputs := []InputCommand{
		{PlayerID: "red-1", MoveDir: Vector2{X: 1, Y: 0.25}},
		{PlayerID: "blue-1", MoveDir: Vector2{X: -1, Y: -0.25}},
		{PlayerID: "red-2", MoveDir: Vector2{X: 0.25, Y: -1}},
		{PlayerID: "blue-2", MoveDir: Vector2{X: -0.25, Y: 1}},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state.Step(inputs)
	}
}

func benchmarkCollisionMap(width, height int) MapData {
	rows := make([][]TileType, height)
	for y := range rows {
		rows[y] = make([]TileType, width)
		for x := range rows[y] {
			if x == 0 || y == 0 || x == width-1 || y == height-1 {
				rows[y][x] = TileWall
			} else if (x*31+y*17)%37 == 0 {
				rows[y][x] = TileWall
			}
		}
	}
	return MapData{Width: width, Height: height, MaxPlayers: 6, TileSize: TileSize, Map: rows}
}
