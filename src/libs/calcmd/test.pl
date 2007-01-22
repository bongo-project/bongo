#!/usr/bin/perl

 #****************************************************************************
 #
 # Copyright (c) 2005, 2006 Novell, Inc.
 # All Rights Reserved.
 #
 # This program is free software; you can redistribute it and/or
 # modify it under the terms of version 2.1 of the GNU Lesser General Public
 # License as published by the Free Software Foundation.
 #
 # This program is distributed in the hope that it will be useful,
 # but WITHOUT ANY WARRANTY; without even the implied warranty of
 # MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 # GNU Lesser General Public License for more details.
 #
 # You should have received a copy of the GNU Lesser General Public License
 # along with this program; if not, contact Novell, Inc.
 #
 # To contact Novell about this file by physical or electronic mail,
 # you may find current contact information at www.novell.com
 #
 #****************************************************************************

# test.pl
#
# A test driver for calcmd library
#
# Usage: 
#
#     test.pl < test.dat
#
# See also:
#
#     Makefile.test and test.dat
#

$passed = 0;
$total = 0;
$failed = 0;

while (<>) {
    if (/^#/) {
        next;
    }
    ($begin, $end, $text, $input) = 
        /(\d\d\d\d-\d\d-\d\d\s\d\d:\d\d)\s(\d\d\d\d-\d\d-\d\d\s\d\d:\d\d)\s+(.*?)\s+>([^#]+)#?.*/;

    $escapedinput = $input;
    $escapedinput =~ s/(["' ])/\\\1/g;

    $result = `./parsecmd $escapedinput`;

    ($rbegin, $rend, $rtext) = $result =~
            /^(\d\d\d\d-\d\d-\d\d\s\d\d:\d\d)\s(\d\d\d\d-\d\d-\d\d\s\d\d:\d\d)\s+(.*?)$/;

    if ($rbegin ne $begin || $rend ne $end || $rtext ne $text) {
        print "FAILED $input (";
        if ($rbegin ne $begin) {
            print "begin $rbegin instead of $begin";
        } 
        if ($rend ne $end) {
            print " end $rend instead of $end";
        }
        if ($rtext ne $text) {
            print " text '$rtext' instead of '$text'";
        }
        print ")\n";
        ++$failed;
    } else {
        ++$passed;
#        print "PASSED\n";
    }
    ++$total
}

printf ("%d tests, %d failed, %2d%% pass rate.\n", 
        $total, $failed, 100 * $passed / $total);

exit $failed;
