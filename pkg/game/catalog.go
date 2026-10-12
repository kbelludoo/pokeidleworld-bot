package game

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"log"
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

// FindBestWeaknessHunt encontra a área de caça ideal onde os monstros têm maior fraqueza contra o Pokémon do jogador
func (c *Catalog) FindBestWeaknessHunt(attackerTypes []string, playerLevel int) (CatalogHunt, float64, float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var bestHunt CatalogHunt
	bestScore := -999999.0
	bestOffensive := 1.0
	bestRisk := 1.0
	found := false

	if playerLevel <= 0 {
		playerLevel = 1
	}

	for _, h := range c.data.Hunts {
		// Ignora cidades e rotas de nível 0
		if h.Level <= 0 {
			continue
		}

		// Apenas rotas disponíveis para o nível atual do jogador
		if h.Level > playerLevel {
			continue
		}

		var defenderTypes []string
		if h.Type1 != "" {
			defenderTypes = append(defenderTypes, h.Type1)
		}
		if h.Type2 != "" {
			defenderTypes = append(defenderTypes, h.Type2)
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
