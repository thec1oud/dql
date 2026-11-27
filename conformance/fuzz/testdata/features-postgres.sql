-- The catalog of seed 12.

CREATE TABLE t1 (
  id bigint NOT NULL,
  c1 boolean NOT NULL,
  c2 boolean NOT NULL,
  c3 bigint,
  c4 varchar(1),
  CONSTRAINT t1_pkey PRIMARY KEY (id),
  CONSTRAINT t1_c3_check CHECK (c3 BETWEEN -847 AND -373),
  CONSTRAINT t1_c4_check CHECK (c4 <> '')
);

CREATE TABLE t2 (
  id bigint NOT NULL,
  c1 bigint,
  CONSTRAINT t2_pkey PRIMARY KEY (id)
);

CREATE TABLE t3 (
  id bigint NOT NULL,
  c1 bigint NOT NULL,
  c2 boolean NOT NULL,
  c3 bigint NOT NULL,
  CONSTRAINT t3_pkey PRIMARY KEY (id),
  CONSTRAINT t3_c1_key UNIQUE (c1),
  CONSTRAINT t3_c1_check CHECK (c1 BETWEEN -549 AND 178),
  CONSTRAINT t3_c3_check CHECK (c3 BETWEEN -859 AND -133)
);

CREATE TABLE t4 (
  id bigint NOT NULL,
  c1 numeric(7,4),
  c2 bigint NOT NULL,
  c3 numeric(1,1),
  c4 varchar(39),
  t2_id bigint,
  CONSTRAINT t4_pkey PRIMARY KEY (id),
  CONSTRAINT t4_c2_key UNIQUE (c2),
  CONSTRAINT t4_c2_check CHECK (c2 BETWEEN 358 AND 956),
  CONSTRAINT t4_c4_check CHECK (c4 IN ('ChJ', 'rJDiKsXQ', 'C')),
  CONSTRAINT t4_t2_id_fkey FOREIGN KEY (t2_id) REFERENCES t2 (id)
);

INSERT INTO t1 (id, c1, c2, c3, c4) VALUES
  (1, TRUE, TRUE, -846, 'c'),
  (3, FALSE, FALSE, -588, 'D'),
  (5, FALSE, TRUE, -845, '6'),
  (7, FALSE, TRUE, -771, 'h'),
  (8, TRUE, TRUE, -603, NULL),
  (10, TRUE, FALSE, -572, 'J'),
  (12, FALSE, FALSE, -667, 'z'),
  (14, FALSE, FALSE, -516, 't'),
  (17, FALSE, TRUE, -474, '9'),
  (20, FALSE, TRUE, NULL, NULL),
  (23, TRUE, TRUE, -652, 'X'),
  (26, TRUE, TRUE, NULL, 'C'),
  (28, FALSE, TRUE, -413, 'u'),
  (30, TRUE, TRUE, NULL, NULL),
  (31, TRUE, FALSE, -569, 'q');

INSERT INTO t2 (id, c1) VALUES
  (3, -866549),
  (6, -570342);

INSERT INTO t3 (id, c1, c2, c3) VALUES
  (1, -367, TRUE, -626),
  (2, -69, TRUE, -855),
  (3, -505, TRUE, -713),
  (5, 132, TRUE, -243),
  (8, -413, FALSE, -167),
  (9, -476, TRUE, -736),
  (12, -295, TRUE, -822),
  (13, -192, TRUE, -601),
  (16, -292, TRUE, -340),
  (17, -263, TRUE, -852),
  (19, -75, TRUE, -227),
  (20, -59, TRUE, -758),
  (23, -445, TRUE, -386);

INSERT INTO t4 (id, c1, c2, c3, c4, t2_id) VALUES
  (2, NULL, 537, -0.2, 'ChJ', 6),
  (5, -711.6239, 509, 0.4, 'rJDiKsXQ', 3),
  (8, 421.9544, 613, -0.5, 'rJDiKsXQ', 6),
  (11, 465.7025, 418, -0.2, 'ChJ', 3),
  (12, -51.0606, 610, 0.6, 'rJDiKsXQ', 3),
  (14, -289.1172, 484, 0.0, 'ChJ', NULL),
  (15, -905.7783, 419, -0.4, NULL, 6),
  (16, NULL, 452, 0.7, NULL, 6),
  (19, -119.5082, 404, -0.7, 'C', 6),
  (20, -753.2519, 630, 0.5, 'rJDiKsXQ', 3),
  (22, -842.4012, 697, -0.8, 'rJDiKsXQ', 6),
  (24, -477.2032, 733, -0.6, 'C', NULL),
  (27, -228.8308, 608, NULL, 'ChJ', 6),
  (30, -778.7489, 776, 0.8, 'rJDiKsXQ', 6),
  (31, 459.3520, 801, -0.6, 'ChJ', NULL),
  (34, -310.0419, 752, 0.0, 'ChJ', NULL);
