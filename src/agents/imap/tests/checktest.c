/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#include <bongocheck.h>
#include "uid_test.c"
#ifdef BONGO_HAVE_CHECK
START_TEST(test1) 
{
    // TODO
    fail_unless(1==1);
    // fail_if(1 == 2);
}
END_TEST
// TODO Write your tests above, and/or
// pound-include other tests of your own here.
START_CHECK_SUITE_SETUP("TODO Describe the unit you are testing")
    CREATE_CHECK_CASE   (tc_core  , "Core"   );
    CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
    CHECK_CASE_ADD_TEST (tc_core  , test1    );
    // TODO register additional tests here
    CHECK_CASE_ADD_TEST (tc_core  ,uid_range_to_sequence_range);
END_CHECK_SUITE_SETUP
#else
SKIP_CHECK_TESTS
#endif

/*
 * Use as many test cases as you want. Be sure to register each test with
 * exactly one test case, then register all test cases with the top_suite.
 *
 * \par Name your test files appropriately
 * If you are testing code in files \p big.c, \p bigger.c, and \p
 * biggest.c, then please put your corresponding test cases in files \p
 * big_test.c, \p bigger_test.c, and \p biggest_test.c, respectively.
 * 
 * Remember to pound-include your test source code files in \p
 * checktest.c, and register the tests appropriately.
 *
 * \par Use the template Makefile.am
 * In your \c tests directory, create the file \c Makefile.am with the following contents:
 * \code
 * TESTS = checktest
 * check_PROGRAMS = $(TESTS)
 * checktest_SOURCES = checktest.c
 * checktest_LDADD = @CHECK_LIBS@
 * checktest_INCLUDES = @CHECK_CFLAGS@
 * \endcode
 *
 * \par Modify the parent's Makefile.am
 * Make sure that the parent directory (\c foo in the example above)
 * has a line in its Makefile.am similar to the following:
 * \code
 * SUBDIRS=tests
 * \endcode
 * If not, add it at the top.
 *
 * \par Add your tests directory to the top configure.in file
 * If the path to the code you are testing is \c bongo/src/apps/foo,
 * then your test code will be in directory \c bongo/src/apps/foo/tests.
 * You need to make sure its Makefile is in the list of makefiles
 * generated in \c configure.in (generally the one located in the \c
 * bongo directory). In this example, you would go to the end of \c
 * configure.in and look for the line
 * \code
 * src/apps/foo/tests/Makefile
 * \endcode
 * inside the \c AC_OUTPUT command. If the line is missing, then add it.
 *
 */


