package game

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog.json
var embeddedCatalogJSON []byte

//go:embed species_types.json
var embeddedSpeciesJSON []byte

type CatalogHunt struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Level int    `json:"level"`
	Area  string `json:"area"`
	Type1 string `json:"type1,omitempty"`
	Type2 string `json:"type2,omitempty"`
}

type CatalogBall struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	CatchRate float64 `json:"catchRate"`
	Price     int64   `json:"price"`
}

type CatalogData struct {
	Hunts []CatalogHunt `json:"hunts"`
	Balls []CatalogBall `json:"balls"`
}

type SpeciesTypeInfo struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Type1 string `json:"type1"`
	Type2 string `json:"type2,omitempty"`
}

type Catalog struct {
	data            CatalogData
	huntsBySlug     map[string]CatalogHunt
	ballsByID       map[int]CatalogBall
	speciesByID     map[int]SpeciesTypeInfo
	speciesByName   map[string]SpeciesTypeInfo
	mu              sync.RWMutex
}

var (
	globalCatalog     *Catalog
	globalCatalogOnce sync.Once
)

// GetGlobalCatalog retorna a instância única do catálogo de caçadas, pokébolas e tipos
func GetGlobalCatalog() *Catalog {
	globalCatalogOnce.Do(func() {
		cat := &Catalog{
			huntsBySlug:   make(map[string]CatalogHunt),
			ballsByID:     make(map[int]CatalogBall),
			speciesByID:   make(map[int]SpeciesTypeInfo),
			speciesByName: make(map[string]SpeciesTypeInfo),
		}

		catDataBytes := bytes.TrimPrefix(embeddedCatalogJSON, []byte("\xef\xbb\xbf"))
		if len(catDataBytes) > 0 {
			if err := json.Unmarshal(catDataBytes, &cat.data); err != nil {
				log.Printf("[Catalog] Erro ao analisar catalog.json: %v", err)
			}
			for _, h := range cat.data.Hunts {
				cat.huntsBySlug[strings.ToLower(h.Slug)] = h
			}
			for _, b := range cat.data.Balls {
				cat.ballsByID[b.ID] = b
			}
		}

		spDataBytes := bytes.TrimPrefix(embeddedSpeciesJSON, []byte("\xef\xbb\xbf"))
		if len(spDataBytes) > 0 {
			var speciesList []SpeciesTypeInfo
			if err := json.Unmarshal(spDataBytes, &speciesList); err != nil {
				log.Printf("[Catalog] Erro ao analisar species_types.json: %v", err)
			} else {
				for _, sp := range speciesList {
					cat.speciesByID[sp.ID] = sp
					cat.speciesByName[strings.ToLower(strings.TrimSpace(sp.Name))] = sp
				}
			}
		}

		globalCatalog = cat
	})
	return globalCatalog
}

func (c *Catalog) GetHunt(slug string) (CatalogHunt, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	h, ok := c.huntsBySlug[strings.ToLower(strings.TrimSpace(slug))]
	return h, ok
}

func (c *Catalog) GetHuntName(slug string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if h, ok := c.huntsBySlug[strings.ToLower(strings.TrimSpace(slug))]; ok {
		return h.Name
	}
	return slug
}

func (c *Catalog) GetHuntTypes(slug string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if h, ok := c.huntsBySlug[strings.ToLower(strings.TrimSpace(slug))]; ok {
		var types []string
		if h.Type1 != "" {
			types = append(types, strings.ToUpper(h.Type1))
		}
		if h.Type2 != "" {
			types = append(types, strings.ToUpper(h.Type2))
		}
		return types
	}
	return nil
}

func (c *Catalog) GetBall(id int) (CatalogBall, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.ballsByID[id]
	return b, ok
}

func (c *Catalog) GetAllHunts() []CatalogHunt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make([]CatalogHunt, len(c.data.Hunts))
	copy(res, c.data.Hunts)
	return res
}

// GetPokemonTypes retorna os tipos do Pokémon por ID da espécie ou por nome
func (c *Catalog) GetPokemonTypes(speciesID int, name string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if speciesID > 0 {
		if sp, ok := c.speciesByID[speciesID]; ok {
			var res []string
			if sp.Type1 != "" {
				res = append(res, strings.ToUpper(sp.Type1))
			}
			if sp.Type2 != "" {
				res = append(res, strings.ToUpper(sp.Type2))
			}
			return res
		}
	}

	normName := strings.ToLower(strings.TrimSpace(name))
	if normName != "" {
		if sp, ok := c.speciesByName[normName]; ok {
			var res []string
			if sp.Type1 != "" {
				res = append(res, strings.ToUpper(sp.Type1))
			}
			if sp.Type2 != "" {
				res = append(res, strings.ToUpper(sp.Type2))
			}
			return res
		}
	}

	return nil
}

// GetSpeciesIDByName retorna o ID da espécie pelo nome ou slug, com suporte a apelidos e variações
func (c *Catalog) GetSpeciesIDByName(name string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getSpeciesIDByNameLocked(name)
}

func (c *Catalog) getSpeciesIDByNameLocked(name string) int {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "" {
		return 0
	}
	switch norm {
	case "nidoranfe", "nidoran-f", "nidoranf", "nidoranfemale":
		return 29
	case "nidoranma", "nidoran-m", "nidoranm", "nidoranmale":
		return 32
	case "farfetch_d", "farfetch'd", "farfetchd":
		return 83
	}
	if sp, ok := c.speciesByName[norm]; ok {
		return sp.ID
	}
	return 0
}

// GetSpeciesByID retorna as informações da espécie pelo ID
func (c *Catalog) GetSpeciesByID(id int) (SpeciesTypeInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sp, ok := c.speciesByID[id]
	return sp, ok
}

// GetBestiaryQueue retorna a fila completa e ordenada de espécies para fechar o Bestiário
func (c *Catalog) GetBestiaryQueue(attackerTypes []string, playerLevel int, dexEntries map[int]PokedexSpeciesEntry) []BestiaryQueueItem {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getBestiaryQueueLocked(attackerTypes, playerLevel, dexEntries)
}

func (c *Catalog) getBestiaryQueueLocked(attackerTypes []string, playerLevel int, dexEntries map[int]PokedexSpeciesEntry) []BestiaryQueueItem {
	if playerLevel <= 0 {
		playerLevel = 1
	}

	// Agrupa caçadas por espécie única acessível para o nível do treinador
	bestHuntBySpecies := make(map[int]CatalogHunt)
	for _, h := range c.data.Hunts {
		if h.Level <= 0 || h.Level > playerLevel {
			continue
		}
		spID := c.getSpeciesIDByNameLocked(h.Slug)
		if spID <= 0 {
			spID = c.getSpeciesIDByNameLocked(h.Name)
		}
		if spID <= 0 {
			continue
		}

		existing, ok := bestHuntBySpecies[spID]
		if !ok || h.Level > existing.Level {
			bestHuntBySpecies[spID] = h
		}
	}

	var items []BestiaryQueueItem
	for spID, h := range bestHuntBySpecies {
		var defenderTypes []string
		if h.Type1 != "" {
			defenderTypes = append(defenderTypes, strings.ToUpper(h.Type1))
		}
		if h.Type2 != "" {
			defenderTypes = append(defenderTypes, strings.ToUpper(h.Type2))
		}

		offMult := GetBestOffensiveMultiplier(attackerTypes, defenderTypes)
		risk := GetIncomingRisk(attackerTypes, defenderTypes)

		entry, hasEntry := dexEntries[spID]
		kills := 0
		claimed := false
		canClaim := false
		if hasEntry {
			kills = entry.Kills
			claimed = entry.Claimed
			canClaim = entry.CanClaim || (kills >= 100 && !claimed)
		}

		targetKills := 100
		remaining := targetKills - kills
		if remaining < 0 {
			remaining = 0
		}

		spName := h.Name
		if sp, ok := c.speciesByID[spID]; ok && sp.Name != "" {
			spName = sp.Name
		}

		score := 0.0
		isComplete := claimed || kills >= 100

		if isComplete {
			// Já concluído: joga para o fim da fila com pontuação negativa
			score = -100000.0 - float64(h.Level)
		} else {
			// Pontuação para concluir o Bestiário de forma inteligente:
			// 1. Base sólida para espécies pendentes
			score = 10000.0
			// 2. Vantagem elemental ofensiva (4.0x = +4000, 2.0x = +2000, 1.0x = +1000)
			score += offMult * 1000.0
			// 3. Proximidade de bater a meta: prioriza fechar monstros que já possuem abates (+60 pts por abate)
			score += float64(kills) * 60.0
			// 4. Bônus por nível da rota (mais XP base por monstro)
			score += float64(h.Level) * 25.0
			// 5. Penalidade para rotas de alto risco defensivo
			score -= risk * 150.0
		}

		items = append(items, BestiaryQueueItem{
			SpeciesID:   spID,
			Name:        spName,
			Slug:        h.Slug,
			Level:       h.Level,
			Types:       defenderTypes,
			Offensive:   offMult,
			Risk:        risk,
			Kills:       kills,
			TargetKills: targetKills,
			Remaining:   remaining,
			Claimed:     claimed,
			CanClaim:    canClaim,
			Score:       score,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Level > items[j].Level
	})

	return items
}

// FindBestWeaknessHunt encontra a área de caça ideal onde os monstros têm maior fraqueza contra o Pokémon do jogador
func (c *Catalog) FindBestWeaknessHunt(attackerTypes []string, playerLevel int) (CatalogHunt, float64, float64, bool) {
	return c.FindBestHunt(attackerTypes, playerLevel, nil, false)
}

// FindBestHunt encontra a área de caça ideal considerando fraqueza elemental, nível e progresso do Bestiário (100 abates)
func (c *Catalog) FindBestHunt(attackerTypes []string, playerLevel int, dexEntries map[int]PokedexSpeciesEntry, pokedexMode bool) (CatalogHunt, float64, float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if playerLevel <= 0 {
		playerLevel = 1
	}

	// 1. Se modo Bestiário estiver ativo e houver dados da Pokédex, segue estritamente a fila prioritária do Bestiário
	if pokedexMode && len(dexEntries) > 0 {
		queue := c.getBestiaryQueueLocked(attackerTypes, playerLevel, dexEntries)
		if len(queue) > 0 && queue[0].Score > 0 {
			target := queue[0]
			if h, ok := c.huntsBySlug[strings.ToLower(target.Slug)]; ok {
				return h, target.Offensive, target.Risk, true
			}
		}
	}

	// 2. Modo Padrão / Fallback: busca rota de maior XP por fraqueza e nível
	var bestHunt CatalogHunt
	bestScore := -999999.0
	bestOffensive := 1.0
	bestRisk := 1.0
	found := false

	for _, h := range c.data.Hunts {
		if h.Level <= 0 || h.Level > playerLevel {
			continue
		}

		var defenderTypes []string
		if h.Type1 != "" {
			defenderTypes = append(defenderTypes, strings.ToUpper(h.Type1))
		}
		if h.Type2 != "" {
			defenderTypes = append(defenderTypes, strings.ToUpper(h.Type2))
		}

		score := ScoreMatchup(attackerTypes, defenderTypes, h.Level, playerLevel)

		if score > bestScore {
			bestScore = score
			bestHunt = h
			bestOffensive = GetBestOffensiveMultiplier(attackerTypes, defenderTypes)
			bestRisk = GetIncomingRisk(attackerTypes, defenderTypes)
			found = true
		}
	}

	return bestHunt, bestOffensive, bestRisk, found
}

