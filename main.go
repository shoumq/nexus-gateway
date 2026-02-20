package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	nexusai "nexus/proto/nexusai/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type analyzeRequest struct {
	UserTZ      string       `json:"user_tz"`
	Points      []trackPoint `json:"points"`
	WeekStarts  string       `json:"week_starts"`
	Constraints constraints  `json:"constraints"`
}

type trackPoint struct {
	TS         time.Time `json:"ts"`
	SleepHours float64   `json:"sleep_hours"`
	Mood       float64   `json:"mood"`
	Activity   float64   `json:"activity"`
	Productive float64   `json:"productive"`
}

type constraints struct {
	WorkStartHour int `json:"work_start_hour"`
	WorkEndHour   int `json:"work_end_hour"`
}

type analyzeResponse struct {
	EnergyByHour      map[string]float64 `json:"energy_by_hour"`
	EnergyByWeekday   map[string]float64 `json:"energy_by_weekday"`
	ProductivityModel productivityModel  `json:"productivity_model"`
	BurnoutRisk       burnoutRisk        `json:"burnout_risk"`
	OptimalSchedule   optimalSchedule    `json:"optimal_schedule"`
	LLMInsight        string             `json:"llm_insight"`
	Debug             map[string]any     `json:"debug,omitempty"`
}

type productivityModel struct {
	Weights map[string]float64 `json:"weights"`
	Score   float64            `json:"score"`
}

type burnoutRisk struct {
	Score                 float64  `json:"score"`
	Level                 string   `json:"level"`
	Reasons               []string `json:"reasons"`
	PredictionHorizonDays int      `json:"prediction_horizon_days"`
}

type optimalSchedule struct {
	SuggestedSleepWindow string   `json:"suggested_sleep_window"`
	BestFocusHours       []string `json:"best_focus_hours"`
	BestLightTasksHours  []string `json:"best_light_tasks_hours"`
	RecoveryTips         []string `json:"recovery_tips"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}

	target := os.Getenv("NEXUS_AI_ADDR")
	if target == "" {
		target = "nexus_ai:9091"
	}

	conn, err := grpc.Dial(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc dial: %v", err)
	}
	defer conn.Close()

	client := nexusai.NewAnalyzerServiceClient(conn)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/ai/analyze", func(w http.ResponseWriter, r *http.Request) {
		handleAnalyze(w, r, client)
	})

	log.Printf("gateway listening on :%s", port)
	if err := http.ListenAndServe(":"+port, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func handleAnalyze(w http.ResponseWriter, r *http.Request, client nexusai.AnalyzerServiceClient) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var in analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(in.Points) < 2 {
		http.Error(w, "need at least 2 points for stable analytics", http.StatusBadRequest)
		return
	}

	grpcReq, err := mapRequest(in)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, r)

	resp, err := client.Analyze(ctx, grpcReq)
	if err != nil {
		http.Error(w, "analyze error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out, err := mapResponse(resp)
	if err != nil {
		http.Error(w, "response error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}

func mapRequest(in analyzeRequest) (*nexusai.AnalyzeRequest, error) {
	points := make([]*nexusai.TrackPoint, 0, len(in.Points))
	for _, p := range in.Points {
		if p.TS.IsZero() {
			return nil, errors.New("point ts is required")
		}
		points = append(points, &nexusai.TrackPoint{
			Ts:         timestamppb.New(p.TS),
			SleepHours: p.SleepHours,
			Mood:       p.Mood,
			Activity:   p.Activity,
			Productive: p.Productive,
		})
	}

	req := &nexusai.AnalyzeRequest{
		UserTz:     in.UserTZ,
		Points:     points,
		WeekStarts: in.WeekStarts,
		Constraints: &nexusai.Constraints{
			WorkStartHour: int32(in.Constraints.WorkStartHour),
			WorkEndHour:   int32(in.Constraints.WorkEndHour),
		},
	}
	return req, nil
}

func mapResponse(in *nexusai.AnalyzeResponse) (*analyzeResponse, error) {
	if in == nil {
		return nil, errors.New("empty response")
	}

	energyByHour := make(map[string]float64, len(in.EnergyByHour))
	for k, v := range in.EnergyByHour {
		energyByHour[intKey(k)] = v
	}

	energyByWeekday := make(map[string]float64, len(in.EnergyByWeekday))
	for k, v := range in.EnergyByWeekday {
		energyByWeekday[k] = v
	}

	weights := make(map[string]float64, len(in.ProductivityModel.GetWeights()))
	for k, v := range in.ProductivityModel.GetWeights() {
		weights[k] = v
	}

	out := &analyzeResponse{
		EnergyByHour:    energyByHour,
		EnergyByWeekday: energyByWeekday,
		ProductivityModel: productivityModel{
			Weights: weights,
			Score:   in.ProductivityModel.GetScore(),
		},
		BurnoutRisk: burnoutRisk{
			Score:                 in.BurnoutRisk.GetScore(),
			Level:                 in.BurnoutRisk.GetLevel(),
			Reasons:               append([]string(nil), in.BurnoutRisk.GetReasons()...),
			PredictionHorizonDays: int(in.BurnoutRisk.GetPredictionHorizonDays()),
		},
		OptimalSchedule: optimalSchedule{
			SuggestedSleepWindow: in.OptimalSchedule.GetSuggestedSleepWindow(),
			BestFocusHours:       append([]string(nil), in.OptimalSchedule.GetBestFocusHours()...),
			BestLightTasksHours:  append([]string(nil), in.OptimalSchedule.GetBestLightTasksHours()...),
			RecoveryTips:         append([]string(nil), in.OptimalSchedule.GetRecoveryTips()...),
		},
		LLMInsight: in.GetLlmInsight(),
	}

	if in.Debug != nil {
		m, err := structpbToMap(in.Debug)
		if err != nil {
			return nil, err
		}
		out.Debug = m
	}

	return out, nil
}

func structpbToMap(s *structpb.Struct) (map[string]any, error) {
	if s == nil {
		return nil, nil
	}
	b, err := s.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func intKey(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}

func withAuthMetadata(ctx context.Context, r *http.Request) context.Context {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ctx
	}
	md := metadata.Pairs("authorization", auth)
	return metadata.NewOutgoingContext(ctx, md)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
