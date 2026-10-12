/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// close codes of the live stream: 4000 plus the HTTP status of the reason, e.g. 4401 when the
// token expired and the client should reconnect with a new one
const (
	CloseTokenExpired = websocket.StatusCode(4401)
	closeBase         = 4000
)

// AuthMessage is the first message of a live stream whose client cannot set headers (DBG-08).
type AuthMessage struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// watchInstance godoc
//
//	@Summary		live debug stream of an instance (WebSocket)
//	@Description	Upgrades to a WebSocket and sends model.DebugMessage values as JSON: a snapshot, deltas with sequence numbers, and the end state when the instance ends (DBG-01). The token goes in the Authorization header or, for browsers, as first message {"type":"auth","token":"..."}, never as query parameter (DBG-08). The stream closes with code 4401 when the token expires and with 4000 plus the HTTP status of other errors.
//	@Tags			debugging
//	@Security		Bearer
//	@Param			ns				path	string	true	"namespace"
//	@Param			id				path	string	true	"instance id"
//	@Param			after			query	int		false	"resume after this sequence number"
//	@Param			evaluate_events	query	bool	false	"evaluate the current value of waiting device events (DBG-18)"
//	@Success		101
//	@Failure		400	{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/live [get]
func (h *handlers) watchInstance(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.WatchOptions{After: q.int64("after"), EvaluateEvents: q.str("evaluate_events") == "true"}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	// the token authenticates, not a cookie, so other origins cannot act for a user
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()
	token := token(r)
	if token == "" {
		if !h.settings.VerifiesTokens {
			conn.Close(closeBase+http.StatusUnauthorized, "send the token in the Authorization header")
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var message AuthMessage
		err = wsjson.Read(readCtx, conn, &message)
		cancel()
		if err != nil || message.Token == "" {
			conn.Close(closeBase+http.StatusUnauthorized, "the first message must carry the token")
			return
		}
		token = message.Token
		if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = "Bearer " + token
		}
	}
	// control frames are handled from here on; ctx ends when the client goes away
	ctx = conn.CloseRead(ctx)
	messages, err := h.c.Watch(ctx, token, r.PathValue("ns"), r.PathValue("id"), options)
	if err != nil {
		e := model.AsError(err)
		_ = wsjson.Write(ctx, conn, model.DebugMessage{Type: model.DebugError, Error: e.Message, ErrorKind: e.Kind})
		conn.Close(websocket.StatusCode(closeBase+e.Kind.HttpStatus()), reason(e.Message))
		return
	}
	var expired <-chan time.Time
	if expiry, ok := auth.Expiry(token); ok {
		timer := time.NewTimer(time.Until(expiry))
		defer timer.Stop()
		expired = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-expired:
			conn.Close(CloseTokenExpired, "token expired")
			return
		case message, ok := <-messages:
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = wsjson.Write(writeCtx, conn, message)
			cancel()
			if err != nil {
				return
			}
			switch message.Type {
			case model.DebugEnd:
				conn.Close(websocket.StatusNormalClosure, "instance ended")
				return
			case model.DebugError:
				conn.Close(websocket.StatusCode(closeBase+http.StatusInternalServerError), reason(message.Error))
				return
			}
		}
	}
}

// reason shortens a close reason to the 123 bytes a close frame allows
func reason(message string) string {
	if len(message) > 120 {
		return message[:120]
	}
	return message
}
