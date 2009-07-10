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

/** \defgroup BongoCheck The Check Framework in the Bongo Project 
 * \brief Provides unit testing for C code in the Bongo Project.
 *
 * The Bongo project uses the Check framework for testing C
 * code. For more information on Check consult its project website at
 * http://check.sourceforge.net/ . Many parts of the Check system are
 * wrapped up in macros found in the bongocheck.h header file. Specific
 * parts of the Check framework are to be used directly, and are listed
 * in the section \ref TheCheckFrameWork.
 * 
 * Be sure to read the documentation for the \ref TheCheckFrameWork and
 * \ref CheckStyleAndMake modules before attempting to write unit tests.
 *
 * Here is an example of a basic test souce code file to get you started.
 * \code
 * #include <include/bongocheck.h>
 * #ifdef BONGO_HAVE_CHECK
 * START_TEST(test1) 
 * {
 *     fail_unless(1==1);
 *     fail_if(1 == 2);
 *     if ( 1 == 2 ) {
 *         fail("1 shouldn't equal 2");
 *     }
 * }
 * END_TEST
 * // pound-include other tests of your own here.
 * START_CHECK_SUITE_SETUP("My test suite description")
 *     CREATE_CHECK_CASE   (tc_core  , "Core"   );
 *     CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
 *     CHECK_CASE_ADD_TEST (tc_core  , test1    );
 *     // register included tests here.
 * END_CHECK_SUITE_SETUP
 * #else
 * SKIP_CHECK_TESTS
 * #endif
 * \endcode
 * 
 * @{
 */

/** \file bongocheck.h
 * \brief Macros that help keep the use of the Check framework consistent.
 *
 * Even if you are adept at Check, please use these macros when
 * writing programmer tests for your code.  If you want to access
 * a piece of Check's functionality that is not currently available
 * through these macros or listed in \link TheCheckFrameWork The Check
 * Framework\endlink, then please propose additional macros rather than
 * using Check directly.
 * 
 */
#include <config.h>
#include <stdlib.h> 
#include <stdio.h> 

/* Just for documentation */
#ifdef RUNNING_DOXYGEN
/** Conditionally set by Autoconf in config.h.
 * The \p config.h file contains pound-defines that control the behavior
 * of the compilation process. Among them is the \c BONGO_HAVE_CHECK symbol,
 * which will be defined if and only if Check is installed and located
 * on the build system.
 *
 *  When you use the Check framework then be sure to wrap it inside a
 * \code
 * #ifdef BONGO_HAVE_CHECK 
 * // use Check here
 * #else
 * SKIP_CHECK_TESTS
 * #endif
 * \endcode
 * block.
 */
#define BONGO_HAVE_CHECK 

/** Set this symbol before including \c bongocheck.h in order to force tests 
 * to return successfully.  By default, the test program returns a failure
 * code if any of the tests fail.  Defining the FORCE_CHECK_SUCCESS
 * symbol causes the test program to always return success. This prevents
 * a "make check" from stopping on test failures.
 *
 * The logging feature is not affected by this option.
 *
 */
#define FORCE_CHECK_SUCCESS
#endif

#ifdef _BONGO_BONGO_HAVE_CHECK
#include <check.h>
#endif

/** Creates a suite that can be used in a suite runner.
 * \hideinitializer
 * \param id regular C identifier.
 * \param description a string in double quotes.
 * 
 * The suite is given the id \a id, and \a description is used when
 * printing a summary of how the test went. Programmers will normally
 * use \c top_suite provided by the #START_CHECK_SUITE_SETUP
 * macro.
 *
 */
#define CREATE_CHECK_SUITE( id, description ) \
    Suite *id = suite_create( description )

/** Creates a test-case that can be added to a suite 
 * (#CHECK_SUITE_ADD_CASE) and have tests added to it
 * (#CHECK_CASE_ADD_TEST).
 * \hideinitializer
 * \param id regular C identifier.
 * \param description a string in double quotes.
 * 
 * It is given id \a id, and \a description is used in
 * reports to describe it.
 *
 */
#define CREATE_CHECK_CASE( id, description ) \
    TCase *id = tcase_create( description )

/** Adds a test-case to a suite.
 * \hideinitializer
 * \param sid regular C identifier associated with a suite.
 * \param cid regular C identifier associated with a test-case.
 * 
 * The test-case with id \a cid is added to the suite with
 * id \a sid. Normally, you will add all test-cases to the \c top_suite
 * which is automatically created by #START_CHECK_SUITE_SETUP.
 *
 */
#define CHECK_SUITE_ADD_CASE( sid, cid ) \
    suite_add_tcase( sid , cid )

/** Adds a test to a test-case.
 * \hideinitializer
 * \param cid regular C identifier associated with a test-case.
 * \param tid regular C identifier associated with a test.
 * 
 * The test with id \a tid is added to the test-case with
 * id \a cid. Tests must be previously defined in a \code 
 * START_TEST (<id>) {
 *   // ... 
 * } END_TEST \endcode block.
 * Test-cases also must be previously defined (#CREATE_CHECK_CASE).
 * 
 * Group similar tests into test-cases. Add all test-cases
 * to the \c top_suite.
 *
 */
#define CHECK_CASE_ADD_TEST( cid, tid ) \
    tcase_add_test( cid, tid )

/** Creates the \c top_suite and opens the main function.
 * \hideinitializer
 * \param description a string in double quotes.
 *
 * \a description is used to describe the top suite in the results.
 *
 */
#define START_CHECK_SUITE_SETUP(description) \
    int main (void) { \
        int nf; \
        CREATE_CHECK_SUITE(top_suite, description);

/* Just for documentation */
#ifdef RUNNING_DOXYGEN
/** \brief (For framework maintenance) Creates the proper exit code.
 * \param x number of tests failed.
 * 
 * It may be desireable to force the "make check" target to return
 * success so that more tests can be run.  In this case define the
 * #FORCE_CHECK_SUCCESS symbol.
 *
 * \attention This macro is only used in this header file.  It is not
 * part of the public interface.
 * \hideinitializer
 *
 */
#define BONGO_CHECK_EXIT(x) 
#else
/* executed when not running doxygen  */
#ifdef FORCE_CHECK_SUCCESS
#define BONGO_CHECK_EXIT(x) \
    return EXIT_SUCCESS
#else
#define BONGO_CHECK_EXIT(x) \
    return (x == 0) ? EXIT_SUCCESS : EXIT_FAILURE
#endif
/*                                    */
#endif

/** Finishes up the main function.
 * \hideinitializer
 * Call this macro after registering all of your tests and test-cases. It
 * runs the tests, outputs an xml log file, and cleans up before exiting.
 *
 */
#define END_CHECK_SUITE_SETUP \
        SRunner *sr = srunner_create(top_suite); \
        srunner_set_xml ( sr, "checklog.xml" ); \
        srunner_run_all ( sr, CK_ENV); \
        nf = srunner_ntests_failed(sr); \
        srunner_free(sr); \
        BONGO_CHECK_EXIT(nf); \
    }

/** Creates a simple program that informs the user no tests were run.
 * Should be used in the else block of \c #ifdef \c BONGO_HAVE_CHECK.
 * \hideinitializer
 *
 */
#define SKIP_CHECK_TESTS \
    int main (void) { \
        printf("#################################\n"); \
        printf("Check not installed, no tests run\n"); \
        printf("#################################\n"); \
        return EXIT_SUCCESS; \
    }

/** \ingroup BongoCheck
 * \defgroup TheCheckFrameWork The Check Framework
 * \brief Listing of used bits of the Check framework (a consumed,
 * external project).
 *
 * The Bongo project uses the Check framework for programmer tests on C
 * code; however, developers for the project only access certain parts
 * of the Check framework directly. Other parts of the infrastructure
 * are supposed to be accessed through special macros implemented in
 * the bongocheck.h file.
 * 
 * This section does not aim to document Check in full.  Instead, it is a
 * quick reference for the pieces of Check that are meant to be accessed
 * directly in the Bongo project. For complete documentation of Check
 * itself refer to the site homepage at http://check.sourceforge.net/ . 
 *
 * As you learn more about Check you may find pieces of it that you want
 * to use that are not listed here, nor are they accessbile through our
 * special macros.  In that case, please discuss needed functionality
 * with the Bongo QA team.  They will either add it to this list of
 * directly accessible bits, or write a macro for it.
 * 
 * - Assertion-like functions:
 *  - \c fail_unless( expr, msg )
 *  - \c fail_if( expr, msg)
 *  - \c fail( msg )
 * - Boilerplate Macros
 *  - \c START_TEST( testname ) and \c END_TEST
 *
 * Of course, you are also free to set any environment variables that
 * Check honors, such as:
 * 
 * - \c CK_VERBOSITY
 * - \c CK_FORK --- good for avoiding fork in the debugger
 * 
 * Be sure to look at the example code in \ref BongoCheck to get started.
 */

/** \ingroup BongoCheck
 * \defgroup CheckStyleAndMake Style and Make
 * \brief Style guidlines, and instructions for Makefile.am relative to
 * using Check for unit testing.
 * 
 * When writing unit tests please follow these style guidlines.
 * Please direct any suggestions concerning these style guidlines to
 * the Bongo QA team.
 *
 * \par Test all functionality
 * Try to follow the standard "If it isn't tested then it doesn't
 * exist." Writing good tests should take at least as long as writing
 * the code itself.
 *
 * \par Place tests in their own directory
 * If you are testing code in the \p foo directory, then place the tests
 * in the \c foo/tests directory.
 *
 * \par Follow the template for managing the suite
 * Have a file in your \c tests directory named \c checktest.c .  In that file use the following template:
 * \code
 * #include <include/bongocheck.h>
 * #ifdef BONGO_HAVE_CHECK
 * START_TEST(test1) 
 * {
 *     // TODO
 *     // fail_unless(1==1);
 *     // fail_if(1 == 2);
 * }
 * END_TEST
 * // TODO Write your tests above, and/or
 * // pound-include other tests of your own here.
 * START_CHECK_SUITE_SETUP("TODO Describe the unit you are testing")
 *     CREATE_CHECK_CASE   (tc_core  , "Core"   );
 *     CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
 *     CHECK_CASE_ADD_TEST (tc_core  , test1    );
 *     // TODO register additional tests here
 * END_CHECK_SUITE_SETUP
 * #else
 * SKIP_CHECK_TESTS
 * #endif
 * \endcode
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
 * checktest_CFLAGS = @CHECK_CFLAGS@
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

/**
 * @}
 */

