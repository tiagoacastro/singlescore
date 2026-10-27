package service

import (
	"github.com/tiagocastro/singlescore/backend/internal/graph/model"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// toCompetitionModel maps a repository competition to its gqlgen model.
func toCompetitionModel(c repository.Competition) *model.Competition {
	return &model.Competition{
		ID:      c.ID,
		Name:    c.Name,
		Country: c.Country,
		Season:  c.Season,
		LogoURL: c.LogoURL,
	}
}

// toTeamModel maps a repository team to its gqlgen model.
func toTeamModel(t repository.Team) *model.Team {
	elo := t.EloRating
	return &model.Team{
		ID:        t.ID,
		Name:      t.Name,
		ShortName: t.ShortName,
		Country:   t.Country,
		EloRating: &elo,
		LogoURL:   t.LogoURL,
	}
}

// toMatchModel builds the gqlgen Match from pre-fetched competition and
// team maps (no per-row DB round-trip). Child collections start empty;
// only the match detail view fills them.
func toMatchModel(r repository.Match, comps map[string]repository.Competition, teams map[string]repository.Team) *model.Match {
	var finalResult *model.Outcome
	if r.FinalResult != nil {
		fr := model.Outcome(string(*r.FinalResult))
		finalResult = &fr
	}
	return &model.Match{
		ID:                   r.ID,
		Competition:          toCompetitionModel(comps[r.CompetitionID]),
		HomeTeam:             toTeamModel(teams[r.HomeTeamID]),
		AwayTeam:             toTeamModel(teams[r.AwayTeamID]),
		KickoffAt:            r.KickoffAt,
		Status:               model.MatchStatus(string(r.Status)),
		Minute:               r.Minute,
		HomeScore:            r.HomeScore,
		AwayScore:            r.AwayScore,
		FinalResult:          finalResult,
		Provenance:           model.Provenance(string(r.Provenance)),
		Venue:                r.Venue,
		Events:               []*model.MatchEvent{},
		ModelPredictions:     []*model.ModelPrediction{},
		BenchmarkPredictions: []*model.BenchmarkPrediction{},
	}
}

func toEventModel(e repository.MatchEvent) *model.MatchEvent {
	return &model.MatchEvent{
		ID:          e.ID,
		EventType:   e.EventType,
		TeamSide:    e.TeamSide,
		Minute:      e.Minute,
		AddedMinute: e.AddedMinute,
		Description: e.Description,
		EventTime:   e.EventTime,
	}
}

func toModelPredictionModel(p repository.ModelPrediction) *model.ModelPrediction {
	return &model.ModelPrediction{
		ID:                  p.ID,
		ModelName:           p.ModelName,
		ModelVersion:        p.ModelVersion,
		ForecastAt:          p.ForecastAt,
		MatchMinute:         p.MatchMinute,
		HomeScoreAtForecast: p.HomeScoreAtFC,
		AwayScoreAtForecast: p.AwayScoreAtFC,
		PHome:               p.PHome,
		PDraw:               p.PDraw,
		PAway:               p.PAway,
		Provenance:          model.Provenance(string(p.Provenance)),
	}
}

func toMatchCommentaryModel(c repository.MatchCommentary) *model.MatchCommentary {
	return &model.MatchCommentary{
		Sequence:    c.Sequence,
		Minute:      c.Minute,
		AddedMinute: c.AddedMinute,
		Period:      c.Period,
		Kind:        c.Kind,
		TeamSide:    c.TeamSide,
		Text:        c.Text,
	}
}

func toMatchStatsModel(s repository.MatchStats) *model.MatchStats {
	return &model.MatchStats{
		HomeShots:         s.HomeShots,
		AwayShots:         s.AwayShots,
		HomeShotsOnTarget: s.HomeShotsOnT,
		AwayShotsOnTarget: s.AwayShotsOnT,
		HomeCorners:       s.HomeCorners,
		AwayCorners:       s.AwayCorners,
		HomeFouls:         s.HomeFouls,
		AwayFouls:         s.AwayFouls,
		HomeYellowCards:   s.HomeYellow,
		AwayYellowCards:   s.AwayYellow,
		HomeRedCards:      s.HomeRed,
		AwayRedCards:      s.AwayRed,
		HomeXg:            s.HomeXG,
		AwayXg:            s.AwayXG,
		Referee:           s.Referee,
		Source:            s.Source,
	}
}

func toBenchmarkModel(b repository.BenchmarkPrediction) *model.BenchmarkPrediction {
	return &model.BenchmarkPrediction{
		ID:          b.ID,
		Provider:    b.Provider,
		SourceType:  model.BenchmarkSourceType(b.SourceType),
		Horizon:     model.BenchmarkHorizon(b.Horizon),
		ForecastAt:  b.ForecastAt,
		MatchMinute: b.MatchMinute,
		PHome:       b.PHome,
		PDraw:       b.PDraw,
		PAway:       b.PAway,
		Provenance:  model.Provenance(string(b.Provenance)),
	}
}
