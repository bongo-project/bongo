#!/usr/bin/perl

# Converts lsc file to a C source file with the proper definitions
#
# usage: 
#
#  cat foo.lsc | ./logger-format.pl > logger-format.c
#


print << 'EOF';

/* Automatically Generated - Do Not Edit */

#include <config.h>
#include "loggerp.h"
#include <stdio.h>

int
LoggerFormatMessage (int eventId, char *buf, unsigned int len, const char *var_S, const char *var_T, unsigned int var_1, unsigned int var_2, void *var_D, int var_X)
{
    struct in_addr x;
    
    switch (eventId) {
EOF
    
while (<>) {
    chomp;

    if (/^\#/) {
	    next;
    }

    if ($_ eq "") {
	    next;
    }

    @pieces = split /\,/, $_, 13;

    $formatstr = "";
    $args = "";

    $formatstr = $pieces[12];
    chomp $formatstr;
    if ($formatstr eq "") {
	    next;
    }
    $formatstr =~ s/\[\$TC] \$SO\: *//;
    $formatstr =~ s/\\n\"$/\"/;

    $extracode = "";
    $xset = "";

    while ($formatstr =~ /\$(.)(.)/) {
	    $varname = "var_$2";
	
	    if ($1 eq "N") {
	        if ($2 ne "1" && $2 ne "2" && $2 ne "D") {
		        print STDERR "unsupported formatter/type pair: $1$2 in $formatstr\n";
		        exit (1);
	        }
	        if ($2 eq "D") {
		        $varname = "*((int*)var_D)";
	        }
	        $formatstr =~ s/\$(.)(.)/\%d/;
	    } elsif ($1 eq "D") {
	        $formatstr =~ s/\$(.)(.)/%d/;
	    } elsif ($1 eq "T" || $1 eq "D" || $1 eq "R") {
	        print STDERR "unsupported formatter $1 in $formatstr\n";
	        exit (1);
	    } elsif ($1 eq "S") {
	        if ($2 ne "S" && $2 ne "T" && $2 ne "D") {
 		        print STDERR "unsupported formatter/type pair: $1$2 in $formatstr\n";
		        exit (1);
	        }
	        if ($2 eq "D") {
		        $varname = "(char*)var_D";
	        }

	        $formatstr =~ s/\$(.)(.)/\%s/;
	    } elsif ($1 eq "X") {
	        if ($2 ne "1" && $2 ne "2" && $2 ne "I") {
 		        print STDERR "unsupported formatter/type pair: $1$2 in $formatstr\n";
		        exit (1);
	        }		
	        $formatstr =~ s/\$(.)(.)/\%08x/;
            $varname = "eventId";
	    } elsif ($1 eq "I" || $1 eq "i") {
		if ($1 eq "I") {
		    $xset = "        x.s_addr = ntohl($varname);\n";
		} else {
		    $xset = "        x.s_addr = $varname;\n";
		}
		$varname = "inet_ntoa(x)";

	        $formatstr =~ s/\$(.)(.)/\%s/;
	    } elsif ($1 eq "P") {
		$varname = "\"REMOVED\"";
		$formatstr =~ s/\$(.)(.)/\%s/;
	    } else {
	        if ($2 eq "D") {
		    $varname = "(char*)var_D";
	        }

	        $formatstr =~ s/\$(.)(.)/\%s/;
	    }
	    $args = $args . ", $varname";
    }


# C - Platform Agent Date
# A - Audit Service Date
# 1 - Numerical value 1
# 2 - Numerical value 2
# S - Text 1
# T - Text 2
# O - Component
# I - Event ID
# L - Log Level
# M - MIME Hint
# X - Data Size
# D - Data

    
    print "    case 0x$pieces[0] :\n$xset";
    print "        return snprintf (buf, len, $formatstr$args);\n";
}

print "    }\nreturn -1;\n}\n";
