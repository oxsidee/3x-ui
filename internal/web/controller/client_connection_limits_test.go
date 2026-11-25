package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestClientConnectionLimitsHTTP(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	db := database.GetDB()
	node := model.Node{Name: "nl-1", Address: "203.0.113.10", Port: 2053, Enable: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	client := model.ClientRecord{Email: "Ivan", SubID: "s-ivan", UUID: "11111111-1111-4111-8111-111111111111", Enable: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	ib := model.Inbound{UserId: 1, Tag: "n1-in-8080-tcp", Port: 8080, Protocol: model.VLESS, Enable: true, NodeID: &node.Id, Settings: `{"clients":[]}`}
	if err := db.Create(&ib).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: ib.Id}).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewClientController(router.Group("/panel/api/clients"))
	post := func(body string) map[string]any {
		req := httptest.NewRequest(http.MethodPost, "/panel/api/clients/Ivan/connectionLimits", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("response %q: %v", rec.Body.String(), err)
		}
		return out
	}

	out := post(`{"connectionLimits":[{"inboundId":` + itoa(ib.Id) + `,"totalGB":10737418240,"limitIp":2}]}`)
	rows, _ := out["obj"].([]any)
	if out["success"] != true || len(rows) != 1 {
		t.Fatalf("set limits = %v", out)
	}
	row := rows[0].(map[string]any)
	if row["totalGB"] != float64(10737418240) || row["limitIp"] != float64(2) {
		t.Fatalf("saved row = %v", row)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel/api/clients/get/Ivan", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var got struct {
		Obj struct {
			ConnectionLimits []model.ClientInbound `json:"connectionLimits"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Obj.ConnectionLimits) != 1 || got.Obj.ConnectionLimits[0].LimitIP != 2 {
		t.Fatalf("client payload connectionLimits = %+v\n%s", got.Obj.ConnectionLimits, rec.Body.String())
	}

	if out := post(`{"connectionLimits":[{"inboundId":999,"totalGB":1}]}`); out["success"] == true {
		t.Fatalf("an unattached inbound must be rejected: %v", out)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
