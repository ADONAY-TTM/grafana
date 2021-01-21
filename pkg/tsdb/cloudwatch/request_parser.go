package cloudwatch

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/tsdb"
)

// Parses the json queries and returns a requestQuery. The requestQuery has a 1 to 1 mapping to a query editor row
func (e *cloudWatchExecutor) parseQueries(queryContext *tsdb.TsdbQuery, startTime time.Time, endTime time.Time) (map[string]map[string]*cloudWatchQuery, error) {
	requestQueries := make(map[string]map[string]*cloudWatchQuery)
	migrateLegacyQuery(queryContext)
	for i, query := range queryContext.Queries {
		queryType := query.Model.Get("type").MustString()
		if queryType != "timeSeriesQuery" && queryType != "" {
			continue
		}

		refID := query.RefId
		query, err := parseRequestQuery(queryContext.Queries[i].Model, refID, startTime, endTime)
		if err != nil {
			return nil, &queryError{err: err, RefID: refID}
		}

		if _, exist := requestQueries[query.Region]; !exist {
			requestQueries[query.Region] = make(map[string]*cloudWatchQuery, 0)
		}
		requestQueries[query.Region][query.Id] = query // append(requestQueries[query.Region], query)
	}

	return requestQueries, nil
}

func migrateLegacyQuery(queryContext *tsdb.TsdbQuery) (*tsdb.TsdbQuery, error) {
	for _, query := range queryContext.Queries {
		// var statistics string
		_, err := query.Model.Get("statistic").String()
		if err == nil {
			continue
		}

		stats := query.Model.Get("statistics").MustStringArray()

		if len(stats) == 1 {
			query.Model.Del("statistics")
			query.Model.Set("statistic", stats[0])
			continue
		}

		bytes, err := queryContext.Queries[0].Model.MarshalJSON()
		if err != nil {
			return nil, err
		}
		for i := 1; i < len(stats); i++ {
			model, err := simplejson.NewJson(bytes)
			if err != nil {
				return nil, err
			}
			q := &tsdb.Query{
				Model:         model,
				DataSource:    query.DataSource,
				MaxDataPoints: query.MaxDataPoints,
				QueryType:     query.QueryType,
				IntervalMs:    query.IntervalMs,
			}

			q.Model.Set("statistic", stats[i])
			q.RefId = getNextRefIDChar(queryContext.Queries)
			queryContext.Queries = append(queryContext.Queries, q)
		}
		query.Model.Del("statistics")
		query.Model.Set("statistic", stats[0])
	}

	return queryContext, nil
}

func getNextRefIDChar(queries []*tsdb.Query) string {
	letters := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for _, rune := range letters {
		char := string(rune)
		characterTaken := false
		for _, query := range queries {
			if query.RefId == char {
				characterTaken = true
				break
			}
		}

		if !characterTaken {
			return char
		}
	}

	return "NA"
}

func parseRequestQuery(model *simplejson.Json, refId string, startTime time.Time, endTime time.Time) (*cloudWatchQuery, error) {
	plog.Debug("Parsing request query", "query", model)
	reNumber := regexp.MustCompile(`^\d+$`)
	region, err := model.Get("region").String()
	if err != nil {
		return nil, err
	}
	namespace, err := model.Get("namespace").String()
	if err != nil {
		return nil, err
	}
	metricName, err := model.Get("metricName").String()
	if err != nil {
		return nil, err
	}
	dimensions, err := parseDimensions(model)
	if err != nil {
		return nil, err
	}
	statistic, err := model.Get("statistic").String()
	if err != nil {
		return nil, err
	}

	p := model.Get("period").MustString("")
	var period int
	if strings.ToLower(p) == "auto" || p == "" {
		deltaInSeconds := endTime.Sub(startTime).Seconds()
		periods := []int{60, 300, 900, 3600, 21600, 86400}
		datapoints := int(math.Ceil(deltaInSeconds / 2000))
		period = periods[len(periods)-1]
		for _, value := range periods {
			if datapoints <= value {
				period = value
				break
			}
		}
	} else {
		if reNumber.Match([]byte(p)) {
			period, err = strconv.Atoi(p)
			if err != nil {
				return nil, err
			}
		} else {
			d, err := time.ParseDuration(p)
			if err != nil {
				return nil, err
			}
			period = int(d.Seconds())
		}
	}

	id := model.Get("id").MustString("")
	if id == "" {
		id = strings.ToLower(refId)
	}
	expression := model.Get("expression").MustString("")
	alias := model.Get("alias").MustString()
	returnData := !model.Get("hide").MustBool(false)
	queryType := model.Get("type").MustString()
	if queryType == "" {
		// If no type is provided we assume we are called by alerting service, which requires to return data!
		// Note, this is sort of a hack, but the official Grafana interfaces do not carry the information
		// who (which service) called the TsdbQueryEndpoint.Query(...) function.
		returnData = true
	}

	matchExact := model.Get("matchExact").MustBool(true)

	return &cloudWatchQuery{
		RefId:          refId,
		Region:         region,
		Id:             id,
		Namespace:      namespace,
		MetricName:     metricName,
		Stats:          statistic,
		Expression:     expression,
		ReturnData:     returnData,
		Dimensions:     dimensions,
		Period:         period,
		Alias:          alias,
		MatchExact:     matchExact,
		UsedExpression: "",
		DeepLink:       "",
	}, nil
}

func parseStatistics(model *simplejson.Json) ([]string, error) {
	var statistics []string
	for _, s := range model.Get("statistics").MustArray() {
		statistics = append(statistics, s.(string))
	}

	return statistics, nil
}

func parseDimensions(model *simplejson.Json) (map[string][]string, error) {
	parsedDimensions := make(map[string][]string)
	for k, v := range model.Get("dimensions").MustMap() {
		// This is for backwards compatibility. Before 6.5 dimensions values were stored as strings and not arrays
		if value, ok := v.(string); ok {
			parsedDimensions[k] = []string{value}
		} else if values, ok := v.([]interface{}); ok {
			for _, value := range values {
				parsedDimensions[k] = append(parsedDimensions[k], value.(string))
			}
		} else {
			return nil, errors.New("failed to parse dimensions")
		}
	}

	sortedDimensions := sortDimensions(parsedDimensions)
	return sortedDimensions, nil
}

func sortDimensions(dimensions map[string][]string) map[string][]string {
	sortedDimensions := make(map[string][]string)
	var keys []string
	for k := range dimensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		sortedDimensions[k] = dimensions[k]
	}
	return sortedDimensions
}
