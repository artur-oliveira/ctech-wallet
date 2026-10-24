package v1

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/dynamo"
	"gopkg.aoctech.app/wallet/api/internal/repositories"
)

func pageApp(owner string) *fiber.App {
	app := fiber.New()
	app.Get("/p", func(c fiber.Ctx) error {
		limit, _, err := pageStart(c, owner)
		if err != nil {
			return sendProblem(c, err)
		}
		last := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "w1"}, "sk": &types.AttributeValueMemberS{Value: "e9"}}
		if limit != 100 {
			last = nil
		}
		return sendPage(c, &repositories.Page[string]{Items: []string{"a"}, LastEvaluatedKey: last}, owner)
	})
	return app
}

func getPage(t *testing.T, app *fiber.App, url string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, url, nil))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Lists answer {data, next_cursor}; the limit is capped at the shared policy (100); a cursor is
// bound to its owner and a malformed or foreign one is a 400, never a restart from page 1.
func TestListPagesUseTheSharedEnvelopeAndFailClosedOnBadCursors(t *testing.T) {
	code, body := getPage(t, pageApp("u1"), "/p?limit=100000")
	if code != 200 || !strings.Contains(body, `"data":["a"]`) || !strings.Contains(body, `"next_cursor":"`) {
		t.Fatalf("page = %d %s, want data + next_cursor (limit capped to 100)", code, body)
	}
	if code, body = getPage(t, pageApp("u1"), "/p"); code != 200 || !strings.Contains(body, `"next_cursor":null`) {
		t.Fatalf("last page = %d %s, want next_cursor null", code, body)
	}
	cursor := dynamo.EncodeCursor(map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "w1"}}, "u1")
	if code, _ = getPage(t, pageApp("u1"), "/p?cursor="+cursor); code != 200 {
		t.Fatalf("own cursor = %d, want 200", code)
	}
	if code, _ = getPage(t, pageApp("u2"), "/p?cursor="+cursor); code != http.StatusBadRequest {
		t.Fatalf("another owner's cursor = %d, want 400", code)
	}
	if code, _ = getPage(t, pageApp("u1"), "/p?cursor=not-a-cursor"); code != http.StatusBadRequest {
		t.Fatalf("malformed cursor = %d, want 400", code)
	}
}
