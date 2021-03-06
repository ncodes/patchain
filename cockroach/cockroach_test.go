package cockroach

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/ellcrys/gorm"
	"github.com/ellcrys/patchain"
	"github.com/ellcrys/patchain/cockroach/tables"
	"github.com/ellcrys/util"
	_ "github.com/jinzhu/gorm/dialects/postgres"
	. "github.com/smartystreets/goconvey/convey"
)

var testDB *sql.DB

var dbName = "test_" + strings.ToLower(util.RandString(5))
var conStr = "postgresql://root@localhost:26257?sslmode=disable"
var conStrWithDB = "postgresql://root@localhost:26257/" + dbName + "?sslmode=disable"

func init() {
	var err error
	testDB, err = sql.Open("postgres", conStr)
	if err != nil {
		panic(fmt.Errorf("failed to connect to database: %s", err))
	}
}

func createDb(t *testing.T) error {
	_, err := testDB.Query(fmt.Sprintf("CREATE DATABASE %s;", dbName))
	return err
}

func dropDB(t *testing.T) error {
	_, err := testDB.Query(fmt.Sprintf("DROP DATABASE %s;", dbName))
	return err
}

func clearTable(db *gorm.DB, tables ...string) error {
	_, err := db.CommonDB().Exec("TRUNCATE " + strings.Join(tables, ","))
	if err != nil {
		return err
	}
	return nil
}

type SampleTbl struct {
	Col string
}

func TestCockroach(t *testing.T) {

	if err := createDb(t); err != nil {
		t.Fatalf("failed to create test database. %s", err)
	}
	defer dropDB(t)

	cdb := NewDB()
	cdb.ConnectionString = conStrWithDB
	cdb.NoLogging()

	Convey("Cockroach", t, func() {

		Convey(".Connect", func() {
			Convey("Should successfully connect to database", func() {
				err := cdb.Connect(0, 5)
				So(err, ShouldBeNil)
			})
		})

		Convey(".GetConn", func() {
			Convey("Should successfully return the underlying db connection", func() {
				db := cdb.GetConn()
				So(db, ShouldNotBeNil)
				So(db, ShouldResemble, cdb.db)
			})
		})

		Convey(".SetConn", func() {
			Convey("Should successfully set connection", func() {
				existingConn := cdb.GetConn()
				newConn, err := gorm.Open("postgres", conStrWithDB)
				So(err, ShouldBeNil)
				So(existingConn.(*gorm.DB), ShouldNotResemble, newConn)
				err = cdb.SetConn(newConn)
				So(err, ShouldBeNil)
				existingConn = cdb.GetConn()
				So(existingConn.(*gorm.DB), ShouldResemble, newConn)
			})

			Convey("Should return error if type is invalid", func() {
				err := cdb.SetConn("invalid_type")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldEqual, "connection type not supported. Requires *gorm.DB")
			})
		})

		Convey(".CreateTables", func() {
			Convey("Should successfully create all tables", func() {
				err := cdb.CreateTables()
				So(err, ShouldBeNil)
				db, _ := gorm.Open("postgres", conStrWithDB)
				So(db.HasTable("objects"), ShouldEqual, true)
				db.Close()
			})
		})

		Convey(".getValidObjectFields", func() {
			Convey("Should not include blacklisted fields", func() {
				fields := cdb.GetValidObjectFields()
				So(fields, ShouldNotContain, blacklistedFields)
			})
		})

		Convey(".getDBTxFromOption", func() {
			Convey("Should successfully return database object included in the options", func() {
				_db := &DB{ConnectionString: conStrWithDB}
				opts := []patchain.Option{&patchain.UseDBOption{DB: _db, Finish: true}}
				_db2, finishTx := cdb.getDBTxFromOption(opts, nil)
				So(_db, ShouldResemble, _db2)
				So(finishTx, ShouldEqual, true)
			})

			Convey("Should successfully return the fallback database object when no db option is included in the options", func() {
				_db := &DB{ConnectionString: conStrWithDB}
				opts := []patchain.Option{}
				_db2, finishTx := cdb.getDBTxFromOption(opts, nil)
				So(nil, ShouldResemble, _db2)
				So(finishTx, ShouldEqual, false)
				_db2, finishTx = cdb.getDBTxFromOption(opts, _db)
				So(_db, ShouldResemble, _db2)
				So(finishTx, ShouldEqual, false)
			})
		})

		Convey(".Create", func() {

			Convey("Should successfully create object", func() {
				o := tables.Object{ID: util.UUID4()}
				err := cdb.Create(&o)
				So(err, ShouldBeNil)
				var actual tables.Object
				cdb.db.Where(&o).First(&actual)
				So(o, ShouldResemble, actual)
			})

			Convey("Should be able to use externally created connection", func() {
				cdb.CreateTables()
				dbTx := cdb.Begin()

				o := tables.Object{ID: util.UUID4()}
				o.Init().ComputeHash()
				err := cdb.Create(&o, &patchain.UseDBOption{DB: dbTx})
				So(err, ShouldBeNil)
				dbTx.Rollback()

				count := 0
				cdb.db.Model(&o).Where(&o).Count(&count)
				So(count, ShouldEqual, 0)

				dbTx = cdb.Begin()
				err = cdb.Create(&o, &patchain.UseDBOption{DB: dbTx})
				So(err, ShouldBeNil)
				dbTx.Commit()

				cdb.db.Model(&o).Where(&o).Count(&count)
				So(count, ShouldEqual, 1)
			})

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey(".TransactWithDB", func() {
			Convey("Should rollback a transaction to create an object", func() {
				newDbTx := cdb.NewDB().Begin()
				cdb.TransactWithDB(newDbTx, false, func(db patchain.DB, commit patchain.CommitFunc, rollback patchain.RollbackFunc) error {
					o := tables.Object{ID: util.UUID4()}
					err := db.Create(&o)
					So(err, ShouldBeNil)
					err = rollback()
					So(err, ShouldBeNil)

					o2 := tables.Object{}
					err = cdb.db.Where(&o).Find(&o2).Error
					So(err, ShouldNotBeNil)
					So(err, ShouldResemble, gorm.ErrRecordNotFound)
					return nil
				})
			})

			Convey("Should commit a transaction to create an object", func() {
				newDbTx := cdb.NewDB().Begin()
				cdb.TransactWithDB(newDbTx, false, func(db patchain.DB, commit patchain.CommitFunc, rollback patchain.RollbackFunc) error {
					o := tables.Object{ID: util.UUID4()}
					err := db.Create(&o)
					So(err, ShouldBeNil)
					err = commit()
					So(err, ShouldBeNil)

					o2 := tables.Object{}
					err = cdb.db.Where(&o).Find(&o2).Error
					So(err, ShouldBeNil)
					So(o, ShouldResemble, o2)
					return nil
				})
			})

			Convey("Should rollback a transaction to create an object if tx callback returns an error and rollback is not implicitly called", func() {
				newDbTx := cdb.NewDB().Begin()
				o := tables.Object{ID: util.UUID4()}
				cdb.TransactWithDB(newDbTx, true, func(db patchain.DB, commit patchain.CommitFunc, rollback patchain.RollbackFunc) error {
					err := db.Create(&o)
					So(err, ShouldBeNil)
					return fmt.Errorf("cause a rollback")
				})
				o2 := tables.Object{}
				err := cdb.db.Where(&o).Find(&o2).Error
				So(err, ShouldNotBeNil)
				So(err, ShouldResemble, gorm.ErrRecordNotFound)
			})

			Convey("Should commit a transaction to create an object if tx callback returns nil and commit is not implicitly called", func() {
				newDbTx := cdb.NewDB().Begin()
				o := tables.Object{ID: util.UUID4()}
				cdb.TransactWithDB(newDbTx, true, func(db patchain.DB, commit patchain.CommitFunc, rollback patchain.RollbackFunc) error {
					err := db.Create(&o)
					So(err, ShouldBeNil)
					return nil
				})
				o2 := tables.Object{}
				err := cdb.db.Where(&o).Find(&o2).Error
				So(err, ShouldBeNil)
				So(o, ShouldResemble, o2)
			})

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey(".UpdatePeerHash", func() {
			Convey("Should successfully update peer hash", func() {
				o := tables.Object{ID: util.UUID4()}
				err := cdb.Create(&o)
				So(err, ShouldBeNil)
				So(o.PeerHash, ShouldBeEmpty)
				err = cdb.UpdatePeerHash(o, "peer_hash_abc")
				So(err, ShouldBeNil)

				o = tables.Object{}
				err = cdb.db.Where(&o).Find(&o).Error
				So(err, ShouldBeNil)
				So(o.PeerHash, ShouldEqual, "peer_hash_abc")
			})

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey("Should successfully create bulk objects", func() {
			objs := []*tables.Object{&tables.Object{ID: util.UUID4(), PeerHash: util.RandString(5)}, &tables.Object{ID: util.UUID4(), PeerHash: util.RandString(5)}}
			objs[0].Init().ComputeHash()
			objs[1].Init().ComputeHash()
			objsI, _ := util.ToSliceInterface(objs)
			err := cdb.CreateBulk(objsI)
			So(err, ShouldBeNil)
			var actual tables.Object
			var actual2 tables.Object
			err = cdb.db.Where(objs[0]).First(&actual).Error
			So(objs[0], ShouldResemble, &actual)
			So(err, ShouldBeNil)
			err = cdb.db.Where(objs[1]).First(&actual2).Error
			So(err, ShouldBeNil)
			So(objs[1], ShouldResemble, &actual2)

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey(".GetLast", func() {
			Convey("Should successfully return the last object matching the query", func() {
				obj1 := &tables.Object{Key: "axa", Value: "1", PeerHash: util.RandString(5), PrevHash: util.RandString(5)}
				obj2 := &tables.Object{Key: "axa", Value: "2", PeerHash: util.RandString(5), PrevHash: util.RandString(5)}
				obj3 := &tables.Object{Key: "axa", Value: "3", PeerHash: util.RandString(5), PrevHash: util.RandString(5)}
				objs := []*tables.Object{obj1.Init().ComputeHash(), obj2.Init().ComputeHash(), obj3.Init().ComputeHash()}
				objsI, _ := util.ToSliceInterface(objs)
				_ = objsI
				err := cdb.CreateBulk(objsI)
				So(err, ShouldBeNil)
				var last tables.Object
				err = cdb.GetLast(&tables.Object{Key: "axa"}, &last)
				So(err, ShouldBeNil)
				So(&last, ShouldResemble, objs[2])
			})

			Convey("Should return ErrNoFound if nothing was found", func() {
				var last tables.Object
				err := cdb.GetLast(&tables.Object{Key: util.RandString(5)}, &last)
				So(err, ShouldEqual, patchain.ErrNotFound)
			})

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey(".GetAll", func() {

			Convey("Should return ErrNoFound if nothing was found", func() {
				var all []tables.Object
				err := cdb.GetAll(&tables.Object{Key: util.RandString(5)}, &all)
				So(err, ShouldNotEqual, patchain.ErrNotFound)
				So(len(all), ShouldEqual, 0)
			})

			Convey("Should successfully return objects", func() {
				key := util.RandString(5)
				objs := []*tables.Object{
					{ID: util.UUID4(), Key: key, PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
					{ID: util.UUID4(), Key: key, PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
				}
				objs[0].Init().ComputeHash()
				objs[1].Init().ComputeHash()
				objsI, _ := util.ToSliceInterface(objs)
				err := cdb.CreateBulk(objsI)

				So(err, ShouldBeNil)
				var all []tables.Object
				err = cdb.GetAll(&tables.Object{Key: key}, &all)
				So(err, ShouldNotEqual, patchain.ErrNotFound)
				So(len(all), ShouldEqual, 2)
			})

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey(".Count", func() {
			Convey("Should successfully count objects that match a query", func() {
				key := util.RandString(5)
				objs := []*tables.Object{
					{ID: util.UUID4(), Key: key, PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
					{ID: util.UUID4(), Key: key, PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
				}
				objs[0].Init().ComputeHash()
				objs[1].Init().ComputeHash()
				objsI, _ := util.ToSliceInterface(objs)
				err := cdb.CreateBulk(objsI)

				So(err, ShouldBeNil)
				var count int64
				err = cdb.Count(&tables.Object{Key: key}, &count)
				So(err, ShouldBeNil)
				So(count, ShouldEqual, 2)
			})

			Reset(func() {
				clearTable(cdb.GetConn().(*gorm.DB), "objects")
			})
		})

		Convey(".getQueryModifiers - Tests query parameters", func() {
			Convey("KeyStartsWith", func() {
				Convey("Should return the last object with the matching start key", func() {
					key := "special_key_prefix/abc"
					obj := &tables.Object{ID: util.UUID4(), Key: key, PeerHash: util.RandString(5), PrevHash: util.RandString(5)}
					err := cdb.Create(obj)
					So(err, ShouldBeNil)
					conn := cdb.GetConn().(*gorm.DB)
					modifiers := cdb.getQueryModifiers(&tables.Object{
						QueryParams: patchain.QueryParams{
							KeyStartsWith: "special_key_prefix",
						},
					})
					var last tables.Object
					err = conn.Scopes(modifiers...).Last(&last).Error
					So(err, ShouldBeNil)
					So(obj, ShouldResemble, &last)
				})

				Convey("Should return the objects ordered by a field in ascending and descending order", func() {
					objs := []*tables.Object{
						{ID: util.UUID4(), Key: "1", PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
						{ID: util.UUID4(), Key: "2", PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
					}
					objs[0].Init().ComputeHash()
					objs[1].Init().ComputeHash()
					objsI, _ := util.ToSliceInterface(objs)
					err := cdb.CreateBulk(objsI)
					So(err, ShouldBeNil)
					conn := cdb.GetConn().(*gorm.DB)
					modifiers := cdb.getQueryModifiers(&tables.Object{
						QueryParams: patchain.QueryParams{
							OrderBy: "key desc",
						},
					})
					var res []*tables.Object
					err = conn.Scopes(modifiers...).Find(&res).Error
					So(err, ShouldBeNil)
					So(len(objs), ShouldEqual, 2)
					So(res[0], ShouldResemble, objs[1])
					So(res[1], ShouldResemble, objs[0])

					res = []*tables.Object{}
					modifiers = cdb.getQueryModifiers(&tables.Object{
						QueryParams: patchain.QueryParams{
							OrderBy: "key desc",
						},
					})
					err = conn.NewScope(nil).DB().Scopes(modifiers...).Find(&res).Error
					So(err, ShouldBeNil)
					So(len(objs), ShouldEqual, 2)
					So(res[1], ShouldResemble, objs[0])
					So(res[0], ShouldResemble, objs[1])
				})

				Convey("Should use QueryParam.Expr for query if set, instead of the query object", func() {
					key := util.RandString(5)
					obj := &tables.Object{ID: util.UUID4(), Key: key, PeerHash: util.RandString(5), PrevHash: util.RandString(5)}
					err := cdb.Create(obj)
					So(err, ShouldBeNil)
					conn := cdb.GetConn().(*gorm.DB)
					res := []*tables.Object{}
					modifiers := cdb.getQueryModifiers(&tables.Object{
						Key: "some_key",
						QueryParams: patchain.QueryParams{
							Expr: patchain.Expr{
								Expr: "key = ?",
								Args: []interface{}{key},
							},
						},
					})
					err = conn.NewScope(nil).DB().Scopes(modifiers...).Find(&res).Error
					So(err, ShouldBeNil)
					So(len(res), ShouldEqual, 1)
					So(obj, ShouldResemble, res[0])
				})

				Convey("Should limit objects returned if Limit is set", func() {
					objs := []*tables.Object{
						{ID: util.UUID4(), Key: "1", PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
						{ID: util.UUID4(), Key: "2", PeerHash: util.RandString(5), PrevHash: util.RandString(5)},
					}
					objs[0].Init().ComputeHash()
					objs[1].Init().ComputeHash()
					objsI, _ := util.ToSliceInterface(objs)
					err := cdb.CreateBulk(objsI)
					So(err, ShouldBeNil)
					conn := cdb.GetConn().(*gorm.DB)
					modifiers := cdb.getQueryModifiers(&tables.Object{
						QueryParams: patchain.QueryParams{
							Limit:   1,
							OrderBy: "timestamp desc",
						},
					})
					var res []*tables.Object
					err = conn.Scopes(modifiers...).Find(&res).Error
					So(err, ShouldBeNil)
					So(len(res), ShouldEqual, 1)
					So(objs[1], ShouldResemble, res[0])
				})

				Reset(func() {
					clearTable(cdb.GetConn().(*gorm.DB), "objects")
				})
			})
		})
	})
}
