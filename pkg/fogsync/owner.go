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

package fogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// NetworkOwner reads the owner of the network of the gateway; empty while the gateway is not
// paired with a network (FOG-11).
type NetworkOwner func(ctx context.Context) (string, error)

// NetworkInfo reads user_id of the network from the cert-manager of the cloud proxy. It is the
// user its certificate names, whose token the cloud proxy sends with every request of the
// gateway, e.g. to the notifier (FOG-08).
func NetworkInfo(url string) NetworkOwner {
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			return "", fmt.Errorf("GET %v: %v %v", url, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var info struct {
			UserId string `json:"user_id"`
		}
		if err = json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return "", fmt.Errorf("GET %v: %w", url, err)
		}
		return info.UserId, nil
	}
}

// SetNetworkOwner names where the owner of the network is read; nil keeps the owners the cloud
// sends (FOG-11).
func (this *Gateway) SetNetworkOwner(source NetworkOwner) {
	this.networkOwner = source
}

// owner is the owner of the network, empty while it is not known
func (this *Gateway) owner() string {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.knownOwner
}

// ownerOr is the owner of the network, or the owner the cloud sent while that is not known
func (this *Gateway) ownerOr(sent string) string {
	if owner := this.owner(); owner != "" {
		return owner
	}
	return sent
}

// refreshOwner takes the owner of the network over when it is due.
func (this *Gateway) refreshOwner(ctx context.Context) error {
	if this.networkOwner == nil {
		return nil
	}
	this.mux.Lock()
	due := this.ownerReadAt.IsZero() || this.now().Sub(this.ownerReadAt) >= this.settings.OwnerInterval
	if due {
		this.ownerReadAt = this.now()
	}
	this.mux.Unlock()
	if !due {
		return nil
	}
	return this.TakeOverOwner(ctx)
}

// TakeOverOwner reads the owner of the network and gives it the deployments and instances on
// the gateway, also those it received before (FOG-11). Tick calls it every OwnerInterval.
func (this *Gateway) TakeOverOwner(ctx context.Context) error {
	if this.networkOwner == nil {
		return nil
	}
	owner, err := this.networkOwner(ctx)
	if err != nil {
		return fmt.Errorf("owner of the network: %w", err)
	}
	if owner == "" {
		return nil
	}
	// known before the rewrite, so that what the cloud sends meanwhile gets it as well
	this.mux.Lock()
	this.knownOwner = owner
	this.mux.Unlock()
	var changed int64
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		changed, err = tx.SetGatewayOwner(owner)
		return err
	})
	if err != nil {
		return err
	}
	if changed > 0 {
		this.logger.Info("deployments and instances of the gateway now belong to the owner of its network", "changed", changed)
	}
	return nil
}
