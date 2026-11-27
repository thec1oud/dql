-- The catalog of seed 1.

CREATE TABLE t1 (
  id bigint NOT NULL,
  c1 varchar(10) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  c2 bigint NOT NULL,
  c3 bigint,
  CONSTRAINT t1_pkey PRIMARY KEY (id),
  CONSTRAINT t1_c1_key UNIQUE (c1),
  CONSTRAINT t1_c2_key UNIQUE (c2),
  CONSTRAINT t1_c1_check CHECK (c1 <> ''),
  CONSTRAINT t1_c2_check CHECK (c2 BETWEEN -392 AND -338)
);

CREATE TABLE t2 (
  id bigint NOT NULL,
  c1 varchar(13) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  c2 bigint NOT NULL,
  c3 varchar(33) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  c4 varchar(17) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  c5 varchar(35) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  t1_id bigint,
  CONSTRAINT t2_pkey PRIMARY KEY (id),
  CONSTRAINT t2_c1_key UNIQUE (c1),
  CONSTRAINT t2_c2_check CHECK (c2 BETWEEN 43 AND 486),
  CONSTRAINT t2_t1_id_fkey FOREIGN KEY (t1_id) REFERENCES t1 (id)
);

CREATE TABLE t3 (
  t1_id bigint NOT NULL,
  t2_id bigint NOT NULL,
  c1 varchar(2) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  CONSTRAINT t3_pkey PRIMARY KEY (t1_id, t2_id),
  CONSTRAINT t3_c1_check CHECK (char_length(c1) >= 2),
  CONSTRAINT t3_t1_id_fkey FOREIGN KEY (t1_id) REFERENCES t1 (id),
  CONSTRAINT t3_t2_id_fkey FOREIGN KEY (t2_id) REFERENCES t2 (id)
);

INSERT INTO t1 (id, c1, c2, c3) VALUES
  (3, 'Q44', -356, 411437),
  (4, 'Ar 0 vew', -391, 348027),
  (6, 'a XA8', -365, NULL),
  (9, 'hh1m6HJ', -370, 457817),
  (12, 'TqEUt', -386, NULL);

INSERT INTO t2 (id, c1, c2, c3, c4, c5, t1_id) VALUES
  (3, 'O', 147, 'jaONjy6UWacI', 'j6d', 'sd lwf8 QQWxWOegWqqhd 4Pco', 4),
  (6, '0tbgy87cOc wK', 100, '0E VcA1 A  PkycNxNku', 'HV7K', 'vLxzZUknsg Wd0Kv7Vi9nc x 4SI8hYuN', 6),
  (8, 's OPzrnl8', 68, 'd3 ptP Yp6Eu8DEIjkhxi4AuOH Z', 'dU', 'CE KbnR qh6 RMSecpuGy7l', 9),
  (10, '7wdFGDg', 149, 'H DMWq e58iTN8b', 'of 2El', 'on Tfj  ysByRGKk 4hj5LohZGs t ZaWt', 6),
  (13, 'bUQt', 397, '5Uf9mH4Gm', '', 'vooXnP  Qxab rvqVXvZYw4n1oV z', 4),
  (15, 'HKFv', 81, 'TeAtk1ssIu5Cw5e1Qd6Tb du5wbkMY H', 'hfA exzmRH2C7m', '8Edw6uKAl26PSGmkGLaYDIDw', 12),
  (16, 'nYU9tI', 465, 'fnQY67jvAG3TEgm', 'RpmbDz87f 1', 'K 5', 6),
  (18, 'RlaJw  X fXh', 254, 'uo GUXaOArJGL30ENd 0B8 xY', 'WgfsDKF', 'P T seFZ7tnQM5kZ qASahqoRQ4jo', 6),
  (21, 'XvihXZhVj6x4', 267, '6Qtl', '8', 'njpmq PZp Yn vLimxwaBaZd trE', 6),
  (24, 'Ku5ZMA 2', 96, 'C3dZjnA', 'Q rDglT yCQDEK', 'xSXTm  hT 6tKNIjzOxpSd eIpD', 9),
  (26, 'JNY9 4 4', 377, 'lq TfopvkbI4Noz6 rvMxe I6S0', 'v8h 9uah7vnZaY', 'EE', NULL),
  (28, '', 207, 'IQDj0TMmUeON4g0 fsuHTV1E klW', '', 'AMvywuVCxIGPV3r3r 5LkUB', 6),
  (30, 'DqVH', 170, 'RE1a z EWP0a Ku1a dct tLoZMg', 'IX', 'bIk4K 67k mTRRfXJDl4pQl', NULL),
  (33, 'EVF  M BrSt', 469, 'C3t', 'Bfq fjCFr9HU', '60RoUIdD19dMTO xz', 12),
  (35, 'ZK', 273, 'bThvgHW2CtOFl 0cxNQ39947u  hb5 hm', 'ZA vm', '5 6W XsFn8OXurNYrWBSzR', 12),
  (36, 'GTLpco5D Cg', 150, 'qa4B PjiWi1vHBG 81y', '', '8', 6),
  (38, '8 lZdw CK4', 216, 'WCRYle', '', 'IVKXf7CiRZAjc1gsl6', 6);

INSERT INTO t3 (t1_id, t2_id, c1) VALUES
  (3, 21, 'SV'),
  (12, 6, 'jJ'),
  (12, 15, 'Bv'),
  (3, 33, 'fK'),
  (6, 24, 'L6'),
  (9, 3, 'D0'),
  (12, 3, '4V'),
  (12, 24, 'A4'),
  (12, 33, 'Md'),
  (4, 8, 'NM'),
  (4, 16, '2D'),
  (6, 18, 'J1'),
  (4, 30, 'hc'),
  (12, 26, 'KA'),
  (3, 15, 'g8'),
  (4, 38, 'oQ'),
  (4, 15, 'qU'),
  (4, 10, 'B2');
