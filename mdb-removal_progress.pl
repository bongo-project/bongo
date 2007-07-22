#!/usr/bin/perl -w

my %agents = ('addressbook' => 62, 'alarm' => 6, 'antispam' => 19, 'avirus' => 23,
	'calcmd' => 4, 'collector' => 4, 'connmgr' => 60, 'generic' => 4, 
	'imap' => 60, 'itip' => 4, 'mailprox' => 56, 'pluspack' => 34, 
	'pop' => 29, 'queue' => 109, 'rules' => 40, 'smtp' => 134, 'store' => 61);

my %libraries = ('connmgr' => 7, 'management' => 14, 'mdb' => 212, 'mdb-file' => 713,
	'mdb-odbc' => 620, 'mdb-xldap' => 279,  'msgapi' => 307, 'nmap' => 30, 
	'python' => 329);

my $search = sub {
	my ($dir, $thing, $original) = @_;
	$current = `grep -r "MDB" src/$dir/* 2>/dev/null | grep -v .svn | grep -v matches | wc -l`;
	chomp $current;
	my $percent = (100.0 / $original) * ($original - $current);
	return sprintf("%12s %3d%% (%3d/%3d)", $thing, $percent, $current, $original);
};

my @agents_done = map { $search->("agents/$_", $_, $agents{$_}); } sort keys %agents;

my @libraries_done = map { $search->("libs/$_", $_, $libraries{$_}); } sort keys %libraries;

for(my $i = 0; $i <= $#agents_done; $i++) {
	$a = $agents_done[$i] || '';
	$b = $libraries_done[$i] || '';
	printf("%20s %20s\n", $a, $b);
}

my $current_lines = `grep -r "MDB" * | grep -v \.svn | grep -v matches | wc -l`;
my $start_lines = 3728;
my $real_start_lines = $start_lines - (212 + 713 + 620 + 279 + 329);

my $completion = (100.0 / $start_lines) * ($start_lines - $current_lines);
my $real_complete = (100.0 / $real_start_lines) * ($start_lines - $current_lines);

printf "TOTAL %3.2f%% ('real': %3.2f%%)\n", $completion, $real_complete;
