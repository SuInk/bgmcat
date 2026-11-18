package app

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var bangumiCatalogSearchURL = "https://api.bgm.tv/v0/search/subjects"

type CatalogQuery struct {
	Keyword   string
	Format    string
	Region    string
	Status    string
	Year      string
	Season    string
	Genre     string
	MinRating float64
	Sort      string
	Limit     int
	Offset    int
}

func parseCatalogQuery(q url.Values, refNow time.Time) CatalogQuery {
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 24
	} else if limit > 48 {
		limit = 48
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}

	format := strings.TrimSpace(q.Get("format"))
	switch format {
	case "TV", "剧场版", "OVA", "WEB":
	default:
		format = "all"
	}

	region := strings.ToLower(strings.TrimSpace(q.Get("region")))
	switch region {
	case "jp", "cn", "west":
	default:
		region = "all"
	}

	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	switch status {
	case "airing", "finished", "upcoming":
	default:
		status = "all"
	}

	year := strings.ToLower(strings.TrimSpace(q.Get("year")))
	if year == "" {
		year = "all"
	}

	season := strings.ToLower(strings.TrimSpace(q.Get("season")))
	switch season {
	case "winter", "spring", "summer", "autumn":
	default:
		season = "all"
	}

	genre := strings.TrimSpace(q.Get("genre"))
	if genre == "" {
		genre = "all"
	}

	minRating, _ := strconv.ParseFloat(strings.TrimSpace(q.Get("minRating")), 64)
	if minRating < 0 || minRating > 10 {
		minRating = 0
	}

	sortMode := strings.ToLower(strings.TrimSpace(q.Get("sort")))
	switch sortMode {
	case "heat", "score", "rank", "date", "match":
	default:
		sortMode = "heat"
	}
	_ = refNow

	return CatalogQuery{
		Keyword:   strings.TrimSpace(q.Get("keyword")),
		Format:    format,
		Region:    region,
		Status:    status,
		Year:      year,
		Season:    season,
		Genre:     genre,
		MinRating: minRating,
		Sort:      sortMode,
		Limit:     limit,
		Offset:    offset,
	}
}

func intersectDateBounds(minA, maxExclA, minB, maxExclB string) (string, string) {
	minOut := minA
	if minB != "" && (minOut == "" || minB > minOut) {
		minOut = minB
	}
	maxOut := maxExclA
	if maxExclB != "" && (maxOut == "" || maxExclB < maxOut) {
		maxOut = maxExclB
	}
	return minOut, maxOut
}

func yearSeasonDateBounds(yearSpec, season string, refNow time.Time) (string, string) {
	if yearSpec == "older" {
		return "", "1990-01-01"
	}
	if left, right, ok := strings.Cut(yearSpec, "-"); ok {
		y1, e1 := strconv.Atoi(strings.TrimSpace(left))
		y2, e2 := strconv.Atoi(strings.TrimSpace(right))
		if e1 == nil && e2 == nil && y1 >= 1900 && y2 >= y1 && y2 <= 2100 {
			return fmt.Sprintf("%04d-01-01", y1), fmt.Sprintf("%04d-01-01", y2+1)
		}
		return "", ""
	}
	var targetYear int
	if yearSpec != "" && yearSpec != "all" {
		y, err := strconv.Atoi(yearSpec)
		if err != nil || y < 1900 || y > 2100 {
			return "", ""
		}
		targetYear = y
	} else if season != "all" {
		targetYear = refNow.Year()
	} else {
		return "", ""
	}

	switch season {
	case "winter":
		return fmt.Sprintf("%04d-01-01", targetYear), fmt.Sprintf("%04d-04-01", targetYear)
	case "spring":
		return fmt.Sprintf("%04d-04-01", targetYear), fmt.Sprintf("%04d-07-01", targetYear)
	case "summer":
		return fmt.Sprintf("%04d-07-01", targetYear), fmt.Sprintf("%04d-10-01", targetYear)
	case "autumn":
		return fmt.Sprintf("%04d-10-01", targetYear), fmt.Sprintf("%04d-01-01", targetYear+1)
	default:
		return fmt.Sprintf("%04d-01-01", targetYear), fmt.Sprintf("%04d-01-01", targetYear+1)
	}
}

// buildBangumiCatalogPayload constructs the Bangumi v0 search payload and reports
// whether the date intersection is empty (impossible combination like 2018 + upcoming)
// and whether a strict rank>0 filter was attached.
func buildBangumiCatalogPayload(cq CatalogQuery, refNow time.Time, includeRankFilter bool) (payload Obj, impossible bool, usedRankFilter bool) {
	tags := make([]string, 0, 4)
	if cq.Format != "all" {
		tags = append(tags, cq.Format)
	}
	switch cq.Region {
	case "jp":
		tags = append(tags, "日本")
	case "cn":
		tags = append(tags, "中国")
	case "west":
		tags = append(tags, "欧美")
	}
	if cq.Genre != "" && cq.Genre != "all" {
		tags = append(tags, cq.Genre)
	}

	minDate, maxDateExcl := yearSeasonDateBounds(cq.Year, cq.Season, refNow)
	today := time.Date(refNow.Year(), refNow.Month(), refNow.Day(), 0, 0, 0, 0, time.UTC)
	tomorrowStr := today.AddDate(0, 0, 1).Format("2006-01-02")

	switch cq.Status {
	case "upcoming":
		minDate, maxDateExcl = intersectDateBounds(minDate, maxDateExcl, tomorrowStr, "")
	case "airing":
		airingStart := today.AddDate(0, 0, -105).Format("2006-01-02")
		minDate, maxDateExcl = intersectDateBounds(minDate, maxDateExcl, airingStart, tomorrowStr)
	case "finished":
		finishedCutoff := today.AddDate(0, 0, -84).Format("2006-01-02")
		if cq.Format == "剧场版" {
			finishedCutoff = tomorrowStr
		}
		minDate, maxDateExcl = intersectDateBounds(minDate, maxDateExcl, "", finishedCutoff)
	}

	if cq.Sort == "date" && cq.Status != "upcoming" {
		recentFloor := ""
		if minDate == "" {
			recentFloor = today.AddDate(-2, 0, 0).Format("2006-01-02")
		}
		minDate, maxDateExcl = intersectDateBounds(minDate, maxDateExcl, recentFloor, tomorrowStr)
	}

	if minDate != "" && maxDateExcl != "" && minDate >= maxDateExcl {
		return nil, true, false
	}

	filter := Obj{
		"type": []int{2},
		"nsfw": false,
	}
	if len(tags) > 0 {
		filter["tag"] = tags
	}
	airBounds := make([]string, 0, 2)
	if minDate != "" {
		airBounds = append(airBounds, ">="+minDate)
	}
	if maxDateExcl != "" {
		airBounds = append(airBounds, "<"+maxDateExcl)
	}
	if len(airBounds) > 0 {
		filter["air_date"] = airBounds
	}
	if cq.MinRating > 0 {
		filter["rating"] = []string{">=" + strconv.FormatFloat(cq.MinRating, 'f', -1, 64)}
	}

	bgmSort := cq.Sort
	switch cq.Sort {
	case "date":
		bgmSort = "heat"
	case "score", "rank":
		if cq.Status == "upcoming" {
			bgmSort = "heat"
		} else if includeRankFilter {
			filter["rank"] = []string{">0"}
			usedRankFilter = true
		}
	case "match":
		if cq.Keyword == "" {
			bgmSort = "heat"
		}
	}

	return Obj{
		"keyword": cq.Keyword,
		"sort":    bgmSort,
		"filter":  filter,
	}, false, usedRankFilter
}

func (cq CatalogQuery) cacheKey() string {
	return fmt.Sprintf(
		"catalog:v1:q=%s|f=%s|r=%s|st=%s|y=%s|sn=%s|g=%s|mr=%.1f|s=%s|l=%d|o=%d",
		cq.Keyword, cq.Format, cq.Region, cq.Status, cq.Year, cq.Season, cq.Genre, cq.MinRating, cq.Sort, cq.Limit, cq.Offset,
	)
}

func (a *App) catalogAnime(ctx context.Context, rawQuery url.Values) (Obj, error) {
	refNow := time.Now()
	cq := parseCatalogQuery(rawQuery, refNow)

	result, err := a.discoveryCached(ctx, cq.cacheKey(), 15*time.Minute, 24*time.Hour, func(ctx context.Context) (Obj, error) {
		return a.fetchCatalogAnime(ctx, cq, refNow)
	})
	if err != nil && len(list(result["items"])) == 0 {
		return nil, err
	}
	items := make([]Obj, 0, len(list(result["items"])))
	for _, item := range list(result["items"]) {
		items = append(items, object(item))
	}
	total := num(result["total"])
	hasMore := flag(result["hasMore"])
	return Obj{
		"items":   items,
		"total":   total,
		"offset":  cq.Offset,
		"limit":   cq.Limit,
		"hasMore": hasMore,
	}, nil
}

func (a *App) fetchCatalogAnime(ctx context.Context, cq CatalogQuery, refNow time.Time) (Obj, error) {
	payload, impossible, usedRankFilter := buildBangumiCatalogPayload(cq, refNow, true)
	if impossible {
		return Obj{"items": []Obj{}, "total": 0, "offset": cq.Offset, "limit": cq.Limit, "hasMore": false}, nil
	}

	fetchLimit := cq.Limit
	if cq.Sort == "date" && cq.Offset == 0 && fetchLimit < 48 {
		fetchLimit = 48
	}
	endpoint := fmt.Sprintf("%s?limit=%d&offset=%d", bangumiCatalogSearchURL, fetchLimit, cq.Offset)
	resp, err := a.requestJSON(ctx, "POST", endpoint, payload, nil)
	if err != nil {
		return nil, err
	}
	rawItems := list(resp["data"])
	if len(rawItems) == 0 && cq.Offset == 0 && usedRankFilter {
		fallbackPayload, _, _ := buildBangumiCatalogPayload(cq, refNow, false)
		if fallbackResp, fallbackErr := a.requestJSON(ctx, "POST", endpoint, fallbackPayload, nil); fallbackErr == nil {
			resp = fallbackResp
			rawItems = list(resp["data"])
		}
	}

	items := make([]Obj, 0, len(rawItems))
	for _, raw := range rawItems {
		rawObj := object(raw)
		a.seedBangumiSubject(rawObj)
		items = append(items, animeFromBangumiAt(rawObj, 0, refNow))
	}

	if cq.Sort == "date" && len(items) > 1 {
		ascending := cq.Status == "upcoming"
		slices.SortStableFunc(items, func(left, right Obj) int {
			ld := str(left["airDate"])
			rd := str(right["airDate"])
			if ld == "" && rd == "" {
				return 0
			}
			if ld == "" {
				return 1
			}
			if rd == "" {
				return -1
			}
			if ascending {
				return cmp.Compare(ld, rd)
			}
			return cmp.Compare(rd, ld)
		})
		if len(items) > cq.Limit {
			items = items[:cq.Limit]
		}
	}

	total := num(resp["total"])
	if int(total) < len(items) {
		total = int64(len(items))
	}
	hasMore := int64(cq.Offset+len(items)) < total && len(rawItems) > 0
	return Obj{
		"items":   items,
		"total":   total,
		"offset":  cq.Offset,
		"limit":   cq.Limit,
		"hasMore": hasMore,
	}, nil
}
