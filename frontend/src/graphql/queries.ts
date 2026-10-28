import { gql } from '@apollo/client';

export const DASHBOARD_QUERY = gql`
  query Dashboard {
    dashboard {
      recentFinishedMatches {
        id
        kickoffAt
        status
        homeScore
        awayScore
        provenance
        competition { id name }
        homeTeam { id name shortName }
        awayTeam { id name shortName }
        preMatchPrediction { id pHome pDraw pAway forecastAt }
      }
      modelMetrics {
        modelName
        modelVersion
        sampleSize
        brierScore
        logLoss
        accuracy
        dateRangeFrom
        dateRangeTo
      }
      dataSources {
        provider
        jobName
        lastRunAt
        lastStatus
        recordsIn
        recordsOut
        errorMessage
      }
      systemStatus {
        status
        databaseReachable
        version
        startedAt
      }
    }
  }
`;

export const COMPETITIONS_QUERY = gql`
  query Competitions {
    competitions { id name country }
  }
`;

export const MATCH_LIST_QUERY = gql`
  query Matches(
    $status: MatchStatus
    $competitionId: ID
    $team: String
    $first: Int
    $after: String
  ) {
    matches(
      status: $status
      competitionId: $competitionId
      team: $team
      first: $first
      after: $after
    ) {
      totalCount
      pageInfo { endCursor hasNextPage }
      edges {
        cursor
        node {
          id
          kickoffAt
          status
          homeScore
          awayScore
          provenance
          competition { id name }
          homeTeam { id name shortName }
          awayTeam { id name shortName }
          preMatchPrediction { id pHome pDraw pAway forecastAt }
        }
      }
    }
  }
`;

export const MATCH_DETAIL_QUERY = gql`
  query Match($id: ID!) {
    match(id: $id) {
      id
      kickoffAt
      status
      homeScore
      awayScore
      provenance
      venue
      competition { id name country }
      homeTeam { id name shortName }
      awayTeam { id name shortName }
      events {
        id
        eventType
        teamSide
        minute
        addedMinute
        description
      }
      commentary {
        sequence
        minute
        addedMinute
        period
        kind
        teamSide
        text
      }
      modelPredictions {
        id
        modelName
        modelVersion
        forecastAt
        matchMinute
        homeScoreAtForecast
        awayScoreAtForecast
        pHome
        pDraw
        pAway
      }
      preMatchPrediction { id pHome pDraw pAway forecastAt }
      benchmarkPredictions {
        id
        provider
        sourceType
        horizon
        forecastAt
        matchMinute
        pHome
        pDraw
        pAway
      }
      stats {
        homeShots
        awayShots
        homeShotsOnTarget
        awayShotsOnTarget
        homeCorners
        awayCorners
        homeFouls
        awayFouls
        homeYellowCards
        awayYellowCards
        homeRedCards
        awayRedCards
        homeXg
        awayXg
        referee
        source
      }
    }
  }
`;

export const MODEL_EVALUATION_QUERY = gql`
  query ModelEvaluation($modelName: String, $modelVersion: String) {
    modelEvaluation(modelName: $modelName, modelVersion: $modelVersion) {
      summary {
        modelName
        modelVersion
        sampleSize
        brierScore
        logLoss
        accuracy
        dateRangeFrom
        dateRangeTo
      }
      calibration {
        binLow
        binHigh
        avgPredicted
        observedFrequency
        sampleSize
      }
      byMinuteBucket { minuteBucket brierScore logLoss sampleSize }
      byCompetition {
        competition { id name country }
        brierScore
        logLoss
        accuracy
        sampleSize
        marketBrierScore
        marketLogLoss
      }
      benchmarks {
        provider
        horizon
        sampleSize
        brierScore
        logLoss
        accuracy
      }
      missingBenchmarkCoverage
    }
  }
`;
