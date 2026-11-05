package repository
import (
 "context"
 "testing"
 "errors"
 "github.com/Wei-Shaw/sub2api/ent"
 "github.com/Wei-Shaw/sub2api/internal/service"
 "github.com/stretchr/testify/require"
)
func TestAPIKeyGroupRoutesStorage(t *testing.T) {
 repo, client := newAPIKeyRepoSQLite(t); ctx := context.Background()
 user := mustCreateAPIKeyRepoUser(t,ctx,client,"routes@test.com")
 ids := make([]int64,30)
 for i := range ids { g,err:=client.Group.Create().SetName("route").Save(ctx); require.NoError(t,err); ids[i]=g.ID }
 key:=&service.APIKey{UserID:user.ID,Key:"sk-routes",Name:"routes",Status:service.StatusActive,GroupID:&ids[0],RouteGroupIDs:ids}
 require.NoError(t,repo.Create(ctx,key))
 got,err:=repo.GetByKeyForAuth(ctx,key.Key);require.NoError(t,err); require.Equal(t,ids,got.CandidateGroupIDs())
 keys,err:=repo.ListKeysByGroupID(ctx,ids[29]);require.NoError(t,err);require.Equal(t,[]string{key.Key},keys)
 client.APIKeyGroupRoute.Use(func(next ent.Mutator) ent.Mutator {return ent.MutateFunc(func(ctx context.Context,m ent.Mutation)(ent.Value,error){
  if m.Op().Is(ent.OpCreate) { if p,ok:=m.Field("position");ok && p==2 {return nil,errors.New("injected route write failure")} };return next.Mutate(ctx,m)
 })})
 reversed:=[]int64{ids[29],ids[0]}; key.GroupID=&reversed[0];key.RouteGroupIDs=reversed;key.Name="must-rollback"
 require.Error(t,repo.Update(ctx,key,service.APIKeyUpdateFields{GroupID:true,RouteGroupIDs:true,Name:true}))
 got,err=repo.GetByID(ctx,key.ID);require.NoError(t,err);require.Equal(t,ids,got.CandidateGroupIDs());require.Equal(t,"routes",got.Name);require.Equal(t,ids[0],*got.GroupID)
 got.GroupID=&ids[1];require.NoError(t,repo.Update(ctx,got,service.APIKeyUpdateFields{GroupID:true}))
 got,err=repo.GetByID(ctx,key.ID);require.NoError(t,err);require.Equal(t,[]int64{ids[1]},got.CandidateGroupIDs())
}
