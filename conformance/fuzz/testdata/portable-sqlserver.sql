-- The catalog of seed 1.

CREATE TABLE t1 (
  id bigint NOT NULL,
  c1 nvarchar(10) COLLATE Latin1_General_100_BIN2 NOT NULL,
  c2 bigint NOT NULL,
  c3 bigint,
  CONSTRAINT t1_pkey PRIMARY KEY (id),
  CONSTRAINT t1_c1_key UNIQUE (c1),
  CONSTRAINT t1_c2_key UNIQUE (c2),
  CONSTRAINT t1_c1_check CHECK (c1 <> N''),
  CONSTRAINT t1_c2_check CHECK (c2 BETWEEN -392 AND -338)
);

CREATE TABLE t2 (
  id bigint NOT NULL,
  c1 nvarchar(13) COLLATE Latin1_General_100_BIN2 NOT NULL,
  c2 bigint NOT NULL,
  c3 nvarchar(33) COLLATE Latin1_General_100_BIN2 NOT NULL,
  c4 nvarchar(17) COLLATE Latin1_General_100_BIN2 NOT NULL,
  c5 nvarchar(35) COLLATE Latin1_General_100_BIN2 NOT NULL,
  t1_id bigint,
  CONSTRAINT t2_pkey PRIMARY KEY (id),
  CONSTRAINT t2_c1_key UNIQUE (c1),
  CONSTRAINT t2_c2_check CHECK (c2 BETWEEN 43 AND 486),
  CONSTRAINT t2_t1_id_fkey FOREIGN KEY (t1_id) REFERENCES t1 (id)
);

CREATE TABLE t3 (
  t1_id bigint NOT NULL,
  t2_id bigint NOT NULL,
  c1 nvarchar(2) COLLATE Latin1_General_100_BIN2 NOT NULL,
  CONSTRAINT t3_pkey PRIMARY KEY (t1_id, t2_id),
  CONSTRAINT t3_c1_check CHECK (DATALENGTH(c1) >= 4),
  CONSTRAINT t3_t1_id_fkey FOREIGN KEY (t1_id) REFERENCES t1 (id),
  CONSTRAINT t3_t2_id_fkey FOREIGN KEY (t2_id) REFERENCES t2 (id)
);

INSERT INTO t1 (id, c1, c2, c3) VALUES
  (3, N'Q44', -356, 411437),
  (4, N'Ar 0 vew', -391, 348027),
  (6, N'a XA8', -365, NULL),
  (9, N'hh1m6HJ', -370, 457817),
  (12, N'TqEUt', -386, NULL);

INSERT INTO t2 (id, c1, c2, c3, c4, c5, t1_id) VALUES
  (3, N'O', 147, N'jaONjy6UWacI', N'j6d', N'sd lwf8 QQWxWOegWqqhd 4Pco', 4),
  (6, N'0tbgy87cOc wK', 100, N'0E VcA1 A  PkycNxNku', N'HV7K', N'vLxzZUknsg Wd0Kv7Vi9nc x 4SI8hYuN', 6),
  (8, N's OPzrnl8', 68, N'd3 ptP Yp6Eu8DEIjkhxi4AuOH Z', N'dU', N'CE KbnR qh6 RMSecpuGy7l', 9),
  (10, N'7wdFGDg', 149, N'H DMWq e58iTN8b', N'of 2El', N'on Tfj  ysByRGKk 4hj5LohZGs t ZaWt', 6),
  (13, N'bUQt', 397, N'5Uf9mH4Gm', N'', N'vooXnP  Qxab rvqVXvZYw4n1oV z', 4),
  (15, N'HKFv', 81, N'TeAtk1ssIu5Cw5e1Qd6Tb du5wbkMY H', N'hfA exzmRH2C7m', N'8Edw6uKAl26PSGmkGLaYDIDw', 12),
  (16, N'nYU9tI', 465, N'fnQY67jvAG3TEgm', N'RpmbDz87f 1', N'K 5', 6),
  (18, N'RlaJw  X fXh', 254, N'uo GUXaOArJGL30ENd 0B8 xY', N'WgfsDKF', N'P T seFZ7tnQM5kZ qASahqoRQ4jo', 6),
  (21, N'XvihXZhVj6x4', 267, N'6Qtl', N'8', N'njpmq PZp Yn vLimxwaBaZd trE', 6),
  (24, N'Ku5ZMA 2', 96, N'C3dZjnA', N'Q rDglT yCQDEK', N'xSXTm  hT 6tKNIjzOxpSd eIpD', 9),
  (26, N'JNY9 4 4', 377, N'lq TfopvkbI4Noz6 rvMxe I6S0', N'v8h 9uah7vnZaY', N'EE', NULL),
  (28, N'', 207, N'IQDj0TMmUeON4g0 fsuHTV1E klW', N'', N'AMvywuVCxIGPV3r3r 5LkUB', 6),
  (30, N'DqVH', 170, N'RE1a z EWP0a Ku1a dct tLoZMg', N'IX', N'bIk4K 67k mTRRfXJDl4pQl', NULL),
  (33, N'EVF  M BrSt', 469, N'C3t', N'Bfq fjCFr9HU', N'60RoUIdD19dMTO xz', 12),
  (35, N'ZK', 273, N'bThvgHW2CtOFl 0cxNQ39947u  hb5 hm', N'ZA vm', N'5 6W XsFn8OXurNYrWBSzR', 12),
  (36, N'GTLpco5D Cg', 150, N'qa4B PjiWi1vHBG 81y', N'', N'8', 6),
  (38, N'8 lZdw CK4', 216, N'WCRYle', N'', N'IVKXf7CiRZAjc1gsl6', 6);

INSERT INTO t3 (t1_id, t2_id, c1) VALUES
  (3, 21, N'SV'),
  (12, 6, N'jJ'),
  (12, 15, N'Bv'),
  (3, 33, N'fK'),
  (6, 24, N'L6'),
  (9, 3, N'D0'),
  (12, 3, N'4V'),
  (12, 24, N'A4'),
  (12, 33, N'Md'),
  (4, 8, N'NM'),
  (4, 16, N'2D'),
  (6, 18, N'J1'),
  (4, 30, N'hc'),
  (12, 26, N'KA'),
  (3, 15, N'g8'),
  (4, 38, N'oQ'),
  (4, 15, N'qU'),
  (4, 10, N'B2');
