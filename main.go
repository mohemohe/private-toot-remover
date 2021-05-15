package main

import (
	"context"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/guregu/dynamo"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"github.com/mattn/go-mastodon"
	"log"
	"sync"
	"time"
)

type (
	Env struct {
		DynamoDB struct {
			Table string `default:"private-toot-remover"`
		}
		Mastodon struct {
			Server      string `default:"https://mstdn.plusminus.io"`
			Access struct {
				Token string `default:""`
			}
		}
		Watch struct {
			Interval struct{
				Seconds int64 `default:"600"`
			}
		}
		Delete struct{
			Older struct{
				Toot struct{
					Seconds int64 `default:"3600"`
				}
			}
		}
	}
	Toot struct {
		ID mastodon.ID `dynamo:"id,hash"`
		CreatedAt int64 `dynamo:"created_at"`
	}
)

var (
	me *mastodon.Account
	env *Env
)

func main() {
	_ = godotenv.Load()

	e := new(Env)
	envconfig.MustProcess("", e)
	env = e

	createTable(env.DynamoDB.Table, Toot{})

	client := mastodon.NewClient(&mastodon.Config{
		Server:      env.Mastodon.Server,
		AccessToken: env.Mastodon.Access.Token,
	})

	account, err := client.GetAccountCurrentUser(context.TODO())
	if err != nil {
		panic(err)
	}
	me = account

	wsc := client.NewWSClient()

	log.Println("watch start")
	go connect(wsc)
	go timer(client)

	wg := sync.WaitGroup{}
	wg.Add(1)
	wg.Wait()
}

func timer(client *mastodon.Client) {
	t := time.NewTicker(time.Second * time.Duration(env.Watch.Interval.Seconds))
	for {
		target := time.Now().Add(-time.Second * time.Duration(env.Delete.Older.Toot.Seconds))

		toots := make([]*Toot, 0)
		if err := newTable(env.DynamoDB.Table).Scan().Filter("created_at <= ?", target.Unix()).All(&toots); err != nil {
			log.Println("dynamodb scan error:", err)
			continue
		}

		for _, toot := range toots {
			// FIXME: statusが既に消えていたときの挙動
			log.Println("delete status:", toot.ID)
			if err := client.DeleteStatus(context.TODO(), toot.ID); err != nil {
				if e := newTable(env.DynamoDB.Table).Delete("id", toot.ID).Run(); e != nil {
					log.Println("dynamodb delete error:", err)
				}
			}
		}

		<- t.C
	}
}

func connect(client *mastodon.WSClient) {
	event, err := client.StreamingWSUser(context.Background())
	if err != nil {
		log.Println(err)
		time.Sleep(10 * time.Second)
		go connect(client)
		return
	}

LOOP:
	for {
		e := <-event
		switch e.(type) {
		case *mastodon.UpdateEvent:
			ue := e.(*mastodon.UpdateEvent)
			onUpdate(ue)
			break
		case *mastodon.ErrorEvent:
			log.Println("mastodon error event:", err)
			break LOOP
		}
	}
	go connect(client)
}

func onUpdate(e *mastodon.UpdateEvent) {
	if e.Status.Account.Acct != me.Acct {
		return
	}
	if e.Status.Visibility != "private" {
		return
	}

	log.Println("put toot to dynamodb:", e.Status.ID)
	err := newTable(env.DynamoDB.Table).Put(Toot{
		ID: e.Status.ID,
		CreatedAt: e.Status.CreatedAt.Unix(),
	}).Run()
	if err != nil {
		log.Println("toot put error:", err)
	}
}

func newDB() *dynamo.DB {
	return dynamo.New(session.Must(session.NewSession()))
}

func newTable(name string) dynamo.Table {
	return newDB().Table(name)
}

func createTable(name string, value interface{}) {
	db := newDB()
	_, err := db.Table(name).Describe().Run()
	if err == nil {
		return
	}
	if err := db.CreateTable(name, value).OnDemand(true).Run(); err != nil {
		panic(err)
	}
}
