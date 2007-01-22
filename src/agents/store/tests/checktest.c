#include <include/bongocheck.h>
#ifdef BONGO_HAVE_CHECK

#include "conversations_test.c"
#include "mail_parser_test.c"

// TODO Write your tests above, and/or
// pound-include other tests of your own here.
START_CHECK_SUITE_SETUP("Unit testing the store agent.\n")
    MemoryManagerOpen("checktest.c");
    CREATE_CHECK_CASE   (tc_core  , "Core"   );
    CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
    CHECK_CASE_ADD_TEST (tc_core  , testnormalizesubject    );
    CHECK_CASE_ADD_TEST (tc_core  , testmailparser    );
    // TODO register additional tests here
END_CHECK_SUITE_SETUP
#else
SKIP_CHECK_TESTS
#endif
