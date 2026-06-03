package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ragbot/internal/config"
	"ragbot/internal/session"
)

// WeatherSkill: city -> date -> forecast. The default provider is deterministic
// mock data; provider=open-meteo calls the public Open-Meteo APIs.
type WeatherSkill struct {
	cfg    config.WeatherConfig
	client *http.Client
}

func NewWeatherSkill(cfg config.WeatherConfig) *WeatherSkill {
	return &WeatherSkill{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *WeatherSkill) Name() string        { return "weather" }
func (s *WeatherSkill) Description() string { return "多轮引导：查询天气" }

func (s *WeatherSkill) Match(input string) bool {
	return containsAny(input, []string{"查天气", "天气怎么样", "查询天气", "weather", "查一下天气"})
}

func (s *WeatherSkill) Start(ctx context.Context, sess *session.Session) (string, error) {
	sess.StartSkill(s.Name())
	return "好的，帮你查天气。请问要查哪个城市？（输入“取消”可退出）", nil
}

func (s *WeatherSkill) Handle(ctx context.Context, sess *session.Session, input string) (string, bool, error) {
	input = strings.TrimSpace(input)
	if isCancel(input) {
		sess.EndSkill()
		return "已取消天气查询，回到知识库问答模式。", true, nil
	}

	switch sess.SkillStep {
	case 0:
		sess.SkillData["city"] = input
		sess.SkillStep = 1
		return "好的，查询日期呢？（如：今天 / 明天 / 2026-06-05）", false, nil
	case 1:
		sess.SkillData["date"] = input
		forecast := s.fetch(ctx, sess.SkillData["city"], sess.SkillData["date"])
		sess.EndSkill()
		return forecast + "\n\n已回到知识库问答模式。", true, nil
	}
	sess.EndSkill()
	return "天气流程异常，已重置。", true, nil
}

func (s *WeatherSkill) fetch(ctx context.Context, city, date string) string {
	if s.cfg.Provider == "" || s.cfg.Provider == "mock" {
		return mockForecast(city, date)
	}
	if s.cfg.Provider == "open-meteo" {
		forecast, err := s.openMeteo(ctx, city, date)
		if err == nil {
			return forecast
		}
		return fmt.Sprintf("查询天气失败：%v\n%s", err, mockForecast(city, date))
	}
	return fmt.Sprintf("（provider=%s 暂未实现，回退到 mock 结果）\n%s", s.cfg.Provider, mockForecast(city, date))
}

func mockForecast(city, date string) string {
	// Deterministic pseudo-forecast so demos are reproducible and offline.
	h := fnv.New32a()
	_, _ = h.Write([]byte(city + date))
	n := h.Sum32()
	conditions := []string{"晴", "多云", "阴", "小雨", "阵雨", "雷阵雨"}
	cond := conditions[n%uint32(len(conditions))]
	temp := 12 + int(n%18) // 12..29
	return fmt.Sprintf("【天气（mock）】%s %s：%s，气温约 %d℃。\n（未配置真实天气 API，配置 skills.weather 后可接入 open-meteo 等。）",
		city, date, cond, temp)
}

type geoResp struct {
	Results []struct {
		Name      string  `json:"name"`
		Country   string  `json:"country"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Timezone  string  `json:"timezone"`
	} `json:"results"`
}

type forecastResp struct {
	Timezone string `json:"timezone"`
	Daily    struct {
		Time             []string  `json:"time"`
		WeatherCode      []int     `json:"weather_code"`
		TemperatureMax   []float64 `json:"temperature_2m_max"`
		TemperatureMin   []float64 `json:"temperature_2m_min"`
		PrecipitationSum []float64 `json:"precipitation_sum"`
	} `json:"daily"`
}

func (s *WeatherSkill) openMeteo(ctx context.Context, city, dateInput string) (string, error) {
	location, err := s.geocode(ctx, city)
	if err != nil {
		return "", err
	}
	targetDate, err := resolveDate(dateInput)
	if err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.6f", location.Latitude))
	q.Set("longitude", fmt.Sprintf("%.6f", location.Longitude))
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_sum")
	q.Set("timezone", "auto")
	q.Set("start_date", targetDate)
	q.Set("end_date", targetDate)

	var fr forecastResp
	if err := s.getJSON(ctx, "https://api.open-meteo.com/v1/forecast?"+q.Encode(), &fr); err != nil {
		return "", err
	}
	if len(fr.Daily.Time) == 0 {
		return "", fmt.Errorf("open-meteo returned no forecast for %s", targetDate)
	}
	code := 0
	if len(fr.Daily.WeatherCode) > 0 {
		code = fr.Daily.WeatherCode[0]
	}
	minTemp, maxTemp := 0.0, 0.0
	if len(fr.Daily.TemperatureMin) > 0 {
		minTemp = fr.Daily.TemperatureMin[0]
	}
	if len(fr.Daily.TemperatureMax) > 0 {
		maxTemp = fr.Daily.TemperatureMax[0]
	}
	precip := 0.0
	if len(fr.Daily.PrecipitationSum) > 0 {
		precip = fr.Daily.PrecipitationSum[0]
	}
	return fmt.Sprintf("【天气（open-meteo）】%s, %s %s：%s，气温 %.0f-%.0f℃，降水 %.1f mm。",
		location.Name, location.Country, targetDate, weatherCodeText(code), minTemp, maxTemp, precip), nil
}

func (s *WeatherSkill) geocode(ctx context.Context, city string) (*struct {
	Name      string  `json:"name"`
	Country   string  `json:"country"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
}, error) {
	q := url.Values{}
	q.Set("name", city)
	q.Set("count", "1")
	q.Set("language", "zh")
	q.Set("format", "json")
	var gr geoResp
	if err := s.getJSON(ctx, "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode(), &gr); err != nil {
		return nil, err
	}
	if len(gr.Results) == 0 {
		return nil, fmt.Errorf("未找到城市：%s", city)
	}
	return &gr.Results[0], nil
}

func (s *WeatherSkill) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("weather http %d: %s", resp.StatusCode, string(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode weather response: %w", err)
	}
	return nil
}

func resolveDate(input string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(input))
	now := time.Now()
	switch s {
	case "", "today", "今天":
		return now.Format("2006-01-02"), nil
	case "tomorrow", "明天":
		return now.AddDate(0, 0, 1).Format("2006-01-02"), nil
	case "后天":
		return now.AddDate(0, 0, 2).Format("2006-01-02"), nil
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", fmt.Errorf("日期格式应为 今天、明天 或 YYYY-MM-DD")
	}
	return s, nil
}

func weatherCodeText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1, 2, 3:
		return "少云到多云"
	case 45, 48:
		return "雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67:
		return "雨"
	case 71, 73, 75, 77:
		return "雪"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95, 96, 99:
		return "雷暴"
	default:
		return fmt.Sprintf("天气代码 %d", code)
	}
}
