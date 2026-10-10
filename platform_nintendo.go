package main

import "fmt"

type nintendoPlatform struct{}

func (nintendoPlatform) Authenticate(body []byte, remoteIP string) (playerID, error) {
	id, nnex := playerIdentity(body)
	if ok, _ := admitPlayer(&id, nnex, remoteIP); !ok {
		return playerID{}, fmt.Errorf("platform identity refused")
	}
	return id, nil
}

func (nintendoPlatform) AuthData(id playerID) map[string]string {
	return map[string]string{"nso_subscription_status": "1", "extended_data": fmt.Sprintf("nextendo:%d", id.PID)}
}

func (nintendoPlatform) Policy() platformPolicy { return platformPolicy{} }

func isCTRTitle(title uint32) bool { return title == 5775 || title == 5760 }
