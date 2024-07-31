package recordrequestlog_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"recordrequestlog"
	"testing"
)

func TestDemo(t *testing.T) {

	cfg := recordrequestlog.CreateConfig()
	cfg.Endpoint = "http://172.16.175.162:5081"
	cfg.Authorization = "Basic aGpzYW55dWFueWFAZ21haWwuY29tOlE5RDJjVGp4TWF0SjhWS28="
	cfg.Organization = "default"
	cfg.StreamName = "default"
	cfg.ServerName = "announcement"

	ctx := context.Background()
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {})

	handler, err := recordrequestlog.New(ctx, next, cfg, "demo-plugin")
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()

	// 创建请求参数
	// params := map[string]string{
	// 	"key1": "value1",
	// 	"key2": "value2",
	// }

	// // 将请求参数转换为 JSON
	// jsonData, err := json.Marshal(params)
	// if err != nil {
	// 	t.Fatal(err)
	// }

	// // 创建包含 JSON 数据的请求体
	// reqBody := bytes.NewBuffer(jsonData)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:5003/api/portal/v1/announcement/index", nil)
	if err != nil {
		t.Fatal(err)
	}

	handler.ServeHTTP(recorder, req)

	fmt.Println(req.URL.String())
}
