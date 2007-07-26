#!/usr/bin/perl -w

my $print_addicts = defined($ARGV[0]) && ($ARGV[0] eq 'addicts')? 1 : 0;

my %agents = ('addressbook' => 62, 'alarm' => 6, 'antispam' => 19, 'avirus' => 23,
	'calcmd' => 4, 'collector' => 4, 'connmgr' => 60, 'generic' => 4, 
	'imap' => 60, 'itip' => 4, 'mailprox' => 56, 'pluspack' => 34, 
	'pop' => 29, 'queue' => 109, 'rules' => 40, 'smtp' => 134, 'store' => 61);

my %libraries = ('connmgr' => 7, 'management' => 14, 'mdb' => 212, 'mdb-file' => 713,
	'mdb-odbc' => 620, 'mdb-xldap' => 279,  'msgapi' => 307, 'nmap' => 30, 
	'python' => 329);

my @msgapi = ('MsgGetSystemDirectoryHandle', 'MsgReadConfiguration', 'MsgLibraryInit',
	'MsgDirectoryHandle', 'MsgInit', 'MsgNmapChallenge',
	'MsgFindObject', 'MsgMdbWriteAny', 'MsgMdbAddAny', 'MsgLibraryStart');

my %function_addicts;

my $search = sub {
	my ($dir, $thing, $original) = @_;
	my $filter = ' grep -v .svn | grep -v matches | wc -l';
	$current = `grep -r "MDB" src/$dir/* 2>/dev/null | $filter`;
	chomp $current;
	$current -= 4 if ($thing eq 'store'); # ALARMDB false positives
	my $percent = (100.0 / $original) * ($original - $current);
	if ($print_addicts && $thing !~ /mdb|msgapi|python/) {
		foreach my $func (@msgapi) {
			my $count = `grep -r "$func" src/$dir/* 2>/dev/null | $filter`;
			push (@{$function_addicts{$thing}}, $func) if ($count > 0); 
		}
	}
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

if ($print_addicts) {
	print "\nAddicts:\n";
	foreach my $thing (sort keys %function_addicts) {
		print "$thing: " . join (", ", @{$function_addicts{$thing}}) . "\n";
	}
}
