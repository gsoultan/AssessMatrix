-- people.grp becomes people.group_name. The storm generator names a column's
-- typed handle after the column, and "Grp" is a name every generated package
-- already declares. A rename is a catalogue change: no rows are rewritten.
ALTER TABLE people RENAME COLUMN grp TO group_name;
ALTER TABLE people RENAME CONSTRAINT people_grp_check TO people_group_name_check;
