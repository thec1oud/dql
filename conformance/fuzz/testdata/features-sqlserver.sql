-- The catalog of seed 12.

CREATE TABLE t1 (
  id bigint NOT NULL,
  c1 bit NOT NULL,
  c2 bit NOT NULL,
  c3 bigint,
  c4 nvarchar(1) COLLATE Latin1_General_100_BIN2,
  CONSTRAINT t1_pkey PRIMARY KEY (id),
  CONSTRAINT t1_c3_check CHECK (c3 BETWEEN -847 AND -373),
  CONSTRAINT t1_c4_check CHECK (c4 <> N'')
);

CREATE TABLE t2 (
  id bigint NOT NULL,
  c1 bigint,
  CONSTRAINT t2_pkey PRIMARY KEY (id)
);

CREATE TABLE t3 (
  id bigint NOT NULL,
  c1 bigint NOT NULL,
  c2 bit NOT NULL,
  c3 bigint NOT NULL,
  CONSTRAINT t3_pkey PRIMARY KEY (id),
  CONSTRAINT t3_c1_key UNIQUE (c1),
  CONSTRAINT t3_c1_check CHECK (c1 BETWEEN -549 AND 178),
  CONSTRAINT t3_c3_check CHECK (c3 BETWEEN -859 AND -133)
);

CREATE TABLE t4 (
  id bigint NOT NULL,
  c1 decimal(7,4),
  c2 bigint NOT NULL,
  c3 decimal(1,1),
  c4 nvarchar(39) COLLATE Latin1_General_100_BIN2,
  t2_id bigint,
  CONSTRAINT t4_pkey PRIMARY KEY (id),
  CONSTRAINT t4_c2_key UNIQUE (c2),
  CONSTRAINT t4_c2_check CHECK (c2 BETWEEN 358 AND 956),
  CONSTRAINT t4_c4_check CHECK (c4 IN (N'ChJ', N'rJDiKsXQ', N'C')),
  CONSTRAINT t4_t2_id_fkey FOREIGN KEY (t2_id) REFERENCES t2 (id)
);

INSERT INTO t1 (id, c1, c2, c3, c4) VALUES
  (1, 1, 1, -846, N'c'),
  (3, 0, 0, -588, N'D'),
  (5, 0, 1, -845, N'6'),
  (7, 0, 1, -771, N'h'),
  (8, 1, 1, -603, NULL),
  (10, 1, 0, -572, N'J'),
  (12, 0, 0, -667, N'z'),
  (14, 0, 0, -516, N't'),
  (17, 0, 1, -474, N'9'),
  (20, 0, 1, NULL, NULL),
  (23, 1, 1, -652, N'X'),
  (26, 1, 1, NULL, N'C'),
  (28, 0, 1, -413, N'u'),
  (30, 1, 1, NULL, NULL),
  (31, 1, 0, -569, N'q');

INSERT INTO t2 (id, c1) VALUES
  (3, -866549),
  (6, -570342);

INSERT INTO t3 (id, c1, c2, c3) VALUES
  (1, -367, 1, -626),
  (2, -69, 1, -855),
  (3, -505, 1, -713),
  (5, 132, 1, -243),
  (8, -413, 0, -167),
  (9, -476, 1, -736),
  (12, -295, 1, -822),
  (13, -192, 1, -601),
  (16, -292, 1, -340),
  (17, -263, 1, -852),
  (19, -75, 1, -227),
  (20, -59, 1, -758),
  (23, -445, 1, -386);

INSERT INTO t4 (id, c1, c2, c3, c4, t2_id) VALUES
  (2, NULL, 537, -0.2, N'ChJ', 6),
  (5, -711.6239, 509, 0.4, N'rJDiKsXQ', 3),
  (8, 421.9544, 613, -0.5, N'rJDiKsXQ', 6),
  (11, 465.7025, 418, -0.2, N'ChJ', 3),
  (12, -51.0606, 610, 0.6, N'rJDiKsXQ', 3),
  (14, -289.1172, 484, 0.0, N'ChJ', NULL),
  (15, -905.7783, 419, -0.4, NULL, 6),
  (16, NULL, 452, 0.7, NULL, 6),
  (19, -119.5082, 404, -0.7, N'C', 6),
  (20, -753.2519, 630, 0.5, N'rJDiKsXQ', 3),
  (22, -842.4012, 697, -0.8, N'rJDiKsXQ', 6),
  (24, -477.2032, 733, -0.6, N'C', NULL),
  (27, -228.8308, 608, NULL, N'ChJ', 6),
  (30, -778.7489, 776, 0.8, N'rJDiKsXQ', 6),
  (31, 459.3520, 801, -0.6, N'ChJ', NULL),
  (34, -310.0419, 752, 0.0, N'ChJ', NULL);
