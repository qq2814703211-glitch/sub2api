package service
import (
 "context"
 "encoding/json"
 "testing"
 "github.com/stretchr/testify/require"
)
func TestAPIKeyRoutesContract(t *testing.T){
 id:=int64(7); key:=&APIKey{GroupID:&id};require.Equal(t,[]int64{7},key.CandidateGroupIDs())
 ids,primary,err:=normalizeAPIKeyGroupIDs(nil,[]int64{7,3,7,5});require.NoError(t,err);require.Equal(t,[]int64{7,3,5},ids);require.EqualValues(t,7,*primary)
 _,_,err=normalizeAPIKeyGroupIDs(&id,[]int64{3,7});require.Error(t,err)
 _,_,err=normalizeAPIKeyGroupIDs(nil,[]int64{7,-1});require.Error(t,err)
 many:=make([]int64,30);for i:=range many{many[i]=int64(i+1)};ids,_,err=normalizeAPIKeyGroupIDs(nil,many);require.NoError(t,err);require.Len(t,ids,30)
}
func TestAPIKeyRoutesCacheProjection(t *testing.T){
 svc:=&APIKeyService{};id:=int64(1);key:=&APIKey{ID:8,UserID:2,GroupID:&id,RouteGroupIDs:[]int64{1,2,30},User:&User{ID:2}}
 snap:=svc.snapshotFromAPIKey(context.Background(),key);key.RouteGroupIDs[1]=99
 payload,err:=json.Marshal(snap);require.NoError(t,err);var decoded APIKeyAuthSnapshot;require.NoError(t,json.Unmarshal(payload,&decoded))
 got,ok,err:=svc.applyAuthCacheEntry("sk-test",&APIKeyAuthCacheEntry{Snapshot:&decoded});require.NoError(t,err);require.True(t,ok);require.Equal(t,[]int64{1,2,30},got.CandidateGroupIDs())
 got.RouteGroupIDs[1]=40;require.EqualValues(t,2,decoded.RouteGroupIDs[1])
 decoded.Version=25;_,ok,err=svc.applyAuthCacheEntry("sk-test",&APIKeyAuthCacheEntry{Snapshot:&decoded});require.NoError(t,err);require.False(t,ok)
}
