#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include "../conversations.c"

START_TEST(testnormalizesubject) 
{
    char* result;
    const char* subject = "Re: Fwd: This is a subject to normalize";
    result = NormalizeSubject(subject);
    fail_unless( strcmp(result, "This is a subject to normalize") == 0 );
    // TODO
    // fail_unless(1==1);
    // fail_if(1 == 2);
}
END_TEST


