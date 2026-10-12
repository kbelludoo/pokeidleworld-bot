package game

import (
	"fmt"
	"strings"
)

// Matriz oficial de efetividade de tipos Pokémon (Gerações 6+)
var typeChart = map[string]map[string]float64{
	"NORMAL": {
		"ROCK": 0.5, "STEEL": 0.5, "GHOST": 0.0,
	},
	"FIRE": {
		"FIRE": 0.5, "WATER": 0.5, "GRASS": 2.0, "ICE": 2.0,
		"BUG": 2.0, "ROCK": 0.5, "DRAGON": 0.5, "STEEL": 2.0,
	},
	"WATER": {
		"FIRE": 2.0, "WATER": 0.5, "GRASS": 0.5, "GROUND": 2.0,
		"ROCK": 2.0, "DRAGON": 0.5,
	},
	"GRASS": {
		"FIRE": 0.5, "WATER": 2.0, "GRASS": 0.5, "POISON": 0.5,
		"GROUND": 2.0, "FLYING": 0.5, "BUG": 0.5, "ROCK": 2.0,
		"DRAGON": 0.5, "STEEL": 0.5,
	},
	"ELECTRIC": {
		"WATER": 2.0, "ELECTRIC": 0.5, "GRASS": 0.5, "GROUND": 0.0,
		"FLYING": 2.0, "DRAGON": 0.5,
	},
	"ICE": {
		"FIRE": 0.5, "WATER": 0.5, "GRASS": 2.0, "ICE": 0.5,
		"GROUND": 2.0, "FLYING": 2.0, "DRAGON": 2.0, "STEEL": 0.5,
	},
	"FIGHTING": {
		"NORMAL": 2.0, "ICE": 2.0, "POISON": 0.5, "FLYING": 0.5,
		"PSYCHIC": 0.5, "BUG": 0.5, "ROCK": 2.0, "GHOST": 0.0,
		"DARK": 2.0, "STEEL": 2.0, "FAIRY": 0.5,
	},
	"POISON": {
		"GRASS": 2.0, "POISON": 0.5, "GROUND": 0.5, "ROCK": 0.5,
		"GHOST": 0.5, "STEEL": 0.0, "FAIRY": 2.0,
	},
	"GROUND": {
		"FIRE": 2.0, "ELECTRIC": 2.0, "GRASS": 0.5, "POISON": 2.0,
		"FLYING": 0.0, "BUG": 0.5, "ROCK": 2.0, "STEEL": 2.0,
	},
	"FLYING": {
		"ELECTRIC": 0.5, "GRASS": 2.0, "FIGHTING": 2.0, "BUG": 2.0,
		"ROCK": 0.5, "STEEL": 0.5,
	},
	"PSYCHIC": {
		"FIGHTING": 2.0, "POISON": 2.0, "PSYCHIC": 0.5, "DARK": 0.0,
		"STEEL": 0.5,
	},
	"BUG": {
		"FIRE": 0.5, "GRASS": 2.0, "FIGHTING": 0.5, "POISON": 0.5,
		"FLYING": 0.5, "PSYCHIC": 2.0, "GHOST": 0.5, "STEEL": 0.5,
		"FAIRY": 0.5, "DARK": 2.0,
	},
	"ROCK": {
		"FIRE": 2.0, "ICE": 2.0, "FIGHTING": 0.5, "GROUND": 0.5,
		"FLYING": 2.0, "BUG": 2.0, "STEEL": 0.5,
	},
	"GHOST": {
		"NORMAL": 0.0, "PSYCHIC": 2.0, "GHOST": 2.0, "DARK": 0.5,
	},
	"DRAGON": {
		"DRAGON": 2.0, "STEEL": 0.5, "FAIRY": 0.0,
	},
	"STEEL": {
		"FIRE": 0.5, "WATER": 0.5, "ELECTRIC": 0.5, "ICE": 2.0,
		"ROCK": 2.0, "STEEL": 0.5, "FAIRY": 2.0,
	},
	"FAIRY": {
		"FIRE": 0.5, "FIGHTING": 2.0, "POISON": 0.5, "DRAGON": 2.0,
		"DARK": 2.0, "STEEL": 0.5,
	},
	"DARK": {
		"FIGHTING": 0.5, "PSYCHIC": 2.0, "GHOST": 2.0, "DARK": 0.5,
		"FAIRY": 0.5,
	},
}

// GetSingleTypeEffectiveness retorna a efetividade de um único tipo de ataque contra um tipo defensor
func GetSingleTypeEffectiveness(attackType, defenseType string) float64 {
	atk := strings.ToUpper(strings.TrimSpace(attackType))
	def := strings.ToUpper(strings.TrimSpace(defenseType))

	if targets, ok := typeChart[atk]; ok {
		if mult, exists := targets[def]; exists {
			return mult
		}
	}
	return 1.0 // Neutro
}

// GetAttackMultiplier calcula a efetividade de um ataque contra um defensor com múltiplos tipos
func GetAttackMultiplier(attackType string, defenderTypes []string) float64 {
	total := 1.0
	for _, dt := range defenderTypes {
		if dt == "" {
			continue
		}
		total *= GetSingleTypeEffectiveness(attackType, dt)
	}
	return total
}

// GetBestOffensiveMultiplier calcula o melhor dano que o atacante pode causar no defensor
func GetBestOffensiveMultiplier(attackerTypes, defenderTypes []string) float64 {
	if len(attackerTypes) == 0 {
		return 1.0
	}
	best := 0.0
	for _, at := range attackerTypes {
		if at == "" {
			continue
		}
		mult := GetAttackMultiplier(at, defenderTypes)
		if mult > best {
			best = mult
		}
	}
	if best == 0.0 {
		return 1.0
	}
	return best
}

// GetIncomingRisk calcula o maior dano que o atacante pode receber do defensor
func GetIncomingRisk(attackerTypes, defenderTypes []string) float64 {
	if len(defenderTypes) == 0 {
		return 1.0
	}
	maxRisk := 0.0
	for _, dt := range defenderTypes {
		if dt == "" {
			continue
		}
		risk := GetAttackMultiplier(dt, attackerTypes)
		if risk > maxRisk {
			maxRisk = risk
		}
	}
	if maxRisk <= 0.0 {
		maxRisk = 0.5
	}
	return maxRisk
}

// ScoreMatchup avalia o confronto entre o Pokémon do jogador e o monstro da área de caça
func ScoreMatchup(attackerTypes, defenderTypes []string, huntLevel int, playerLevel int) float64 {
	offensive := GetBestOffensiveMultiplier(attackerTypes, defenderTypes)
	risk := GetIncomingRisk(attackerTypes, defenderTypes)

	// Base de pontuação por fraqueza:
	// 4.0x (fraqueza dupla) = 4000 pts
	// 2.0x (super efetivo)  = 2000 pts
	// 1.0x (neutro)         = 1000 pts
	// 0.5x (pouco efetivo)  = 500 pts
	// 0.25x (resistido)     = 250 pts
	score := offensive * 1000.0

	// Penalidade pelo risco de dano recebido:
	// Se o inimigo nos causa 2.0x, perde 500 pts. Se causa 0.5x, ganha 250 pts
	score -= (risk - 1.0) * 500.0

	// Fator de nível: prioriza áreas com nível mais próximo ao do jogador (desde que <= playerLevel)
	// para obter maior rendimento de EXP e Ouro por monstro derrotado
	if playerLevel > 0 {
		if huntLevel <= playerLevel {
			// Bônus proporcional ao nível da hunt
			score += float64(huntLevel) * 20.0
		} else {
			// Nível superior ao jogador: penalidade pesada pois não é recomendado / pode estar bloqueado
			score -= float64(huntLevel-playerLevel) * 200.0
		}
	}

	return score
}

// FormatMatchupDescription formata uma descrição amigável do confronto
func FormatMatchupDescription(offensiveMult float64, risk float64) string {
	var offText string
	if offensiveMult >= 4.0 {
		offText = "4.0x (Fraqueza Dupla!)"
	} else if offensiveMult >= 2.0 {
		offText = "2.0x (Super Efetivo)"
	} else if offensiveMult == 1.0 {
		offText = "1.0x (Neutro)"
	} else if offensiveMult == 0.5 {
		offText = "0.5x (Pouco Efetivo)"
	} else {
		offText = fmt.Sprintf("%.2fx (Resistido)", offensiveMult)
	}

	var defText string
	if risk >= 2.0 {
		defText = "Alto Risco (Inimigo Super Efetivo)"
	} else if risk <= 0.5 {
		defText = "Baixo Risco (Resistimos aos ataques)"
	} else {
		defText = "Risco Moderado"
	}

	return fmt.Sprintf("%s | %s", offText, defText)
}
