import { gql } from '@apollo/client';

export const DASHBOARD_QUERY = gql`
  query Dashboard {
    dashboard {
      liveMatches {
        id
        kickoffAt
        status
        minute
        homeScore
        awayScore
        provenance
        competition { id name country }
        homeTeam { id name shortName }
        awayTeam { id name shortName }
        latestModelPrediction {
          id
          forecastAt
          pHome
          pDraw
          pAway
        }
      }
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
      }
      recentPredictionChanges {
        deltaHome
        deltaDraw
        deltaAway
        prediction {
          id
          forecastAt
          matchMinute
          pHome
          pDraw
          pAway
        }
        match {
          id
          homeTeam { id shortName name }
          awayTeam { id shortName name }
          homeScore
          awayScore
          minute
          status
        }
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
          latestModelPrediction { id pHome pDraw pAway forecastAt matchMinute }
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
