package repository

import "github.com/redis/go-redis/v9"

// Close before delete so a command whose response timed out cannot grant later.
var releaseFencedAPIKeySlotScript = redis.NewScript(`
redis.replicate_commands()
local t=redis.call('TIME')
local now=tonumber(t[1])*1000+math.floor(tonumber(t[2])/1000)
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',now)
redis.call('ZADD',KEYS[2],now+tonumber(ARGV[2])*1000,ARGV[1])
local last=redis.call('ZREVRANGE',KEYS[2],0,0,'WITHSCORES')
redis.call('PEXPIRE',KEYS[2],math.max(1,tonumber(last[2])-now+5000))
redis.call('ZREM',KEYS[1],ARGV[1])
return 1
`)
