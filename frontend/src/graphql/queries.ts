import { gql } from '@apollo/client';

export const DASHBOARD_QUERY = gql`
  query Dashboard {
    dashboard {
      recentFinishedMatches {
        id
        kickoffAt
        status
        finalResult
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
        demoMode
        version
        startedAt
      }
    }
  }
`;

export const MATCH_LIST_QUERY = gql`
  query Matches($status: MatchStatus, $first: Int, $after: String) {
    matches(status: $status, first: $first, after: $after) {
      totalCount
      pageInfo { endCursor hasNextPage }
      edges {
        cursor
        node {
          id
          kickoffAt
          status
          minute
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
      minute
      homeScore
      awayScore
      finalResult
      provenance
      venue
      competition { id name country season }
      homeTeam { id name shortName eloRating }
      awayTeam { id name shortName eloRating }
      events {
        id
        eventType
        teamSide
        minute
        addedMinute
        description
        eventTime
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
        provenance
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
        competition { id name }
        brierScore
        logLoss
        sampleSize
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
