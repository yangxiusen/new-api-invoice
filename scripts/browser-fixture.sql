-- Only for the explicitly created invoice_test_browser database.
CREATE TABLE users(id bigint PRIMARY KEY,username text,display_name text,email text,role bigint,status bigint,deleted_at timestamptz,auth_version bigint NOT NULL DEFAULT 1,invoice_enabled boolean);
CREATE TABLE top_ups(id bigint PRIMARY KEY,user_id bigint,trade_no varchar(255),money numeric,amount bigint,payment_method varchar(50),payment_provider varchar(50),create_time bigint,complete_time bigint,status text);
INSERT INTO users VALUES(1,'admin','测试管理员','admin@example.test',100,1,NULL,1,false),(2,'user','测试用户','user@example.test',1,1,NULL,1,true),(3,'denied','未开通测试用户','denied@example.test',1,1,NULL,1,false),(4,'disabled','禁用测试用户','disabled@example.test',1,2,NULL,1,false);
INSERT INTO top_ups SELECT i,2,'BROWSER-ORDER-'||i,(10+i::numeric/100),999999,'alipay','epay',1700000000+i,CASE WHEN i%2=0 THEN 0 ELSE 1700000100+i END,'success' FROM generate_series(1,25) i;
INSERT INTO top_ups VALUES(100,3,'DENIED-ORDER',50.00,1,'alipay','epay',1700000000,0,'success');
