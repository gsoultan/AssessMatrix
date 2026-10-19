-- Integrity: a keyed seal on each attempt row over the head of its chain
-- (event count, last hash, purge time). The chain shows an edited or
-- reordered event; the seal also shows events cut off the end and a record
-- marked purged by hand, since neither can be re-sealed without AM_SECRET.
--
-- Existing rows start unsealed. `core migrate` (and `core serve` when given
-- the owner URL) seals each one whose chain still checks, using AM_SECRET;
-- an attempt left unsealed reads as altered.
ALTER TABLE attempts ADD COLUMN seal bytea;
