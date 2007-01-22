#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include <sys/types.h>
#include <dirent.h>

#include "../mail-parser.c"


static int VerboseMode = 1;

typedef struct _HeaderData HeaderData;

struct _HeaderData {
    MailHeaderName header;
    char *headerRaw;

    char *displayName;
    char *address;
    char *data;

    struct _HeaderData *next;
};


/* FIXME - doesn't do much error-checking of the input test case */
static HeaderData *
ParseTestCase(FILE *f)
{
    char buffer[4096];
    HeaderData *hdr = NULL;
    HeaderData *prev = NULL;
    HeaderData *first = NULL;
    char *tokens[10];
    int i;
    char *p;
    char *q;

    while (fgets(buffer, sizeof(buffer), f)) {
        if ('#' == buffer[0]) {
            continue;
        } else if ('@' != buffer[0]) {
            return first;
        }
        hdr = MemMalloc(sizeof(HeaderData));
        
        i = 0;
        for (p = strchr(buffer, '(');
             p;
             p = strchr(q, '('))
        {
            for (q = strchr(p, ')');
                 '\\' == q[-1];
                 q = strchr(q + 1, ')'))
            {}
            *q = 0;
            p++;
            q++;
            tokens[i++] = strlen(p) ? MemStrdup(p) : NULL;
        }


        switch (buffer[1]) {
        case 'F':
            hdr->header = MAIL_FROM;
            goto participant;
        case 'f':
            hdr->header = MAIL_SENDER;
            goto participant;
        case 'R':
            hdr->header = MAIL_REPLY_TO;
            goto participant;
        case 'T':
            hdr->header = MAIL_TO;
            goto participant;
        case 'C':
            hdr->header = MAIL_CC;
            goto participant; 
        case 'B':
            hdr->header = MAIL_BCC;
            goto participant;
        case 'L' :
            hdr->header = MAIL_X_BEEN_THERE;
            goto participant;
        case 'l' :
            hdr->header = MAIL_X_LOOP;
            goto participant;
        participant:
            hdr->displayName = tokens[0];
            hdr->address = tokens[1];
            break;
           
        case 'D':
            hdr->header = MAIL_DATE;
            /* FIXME */
            break;
        case 'S':
            hdr->header = MAIL_SUBJECT;
            hdr->data = tokens[0];
            break;
        case 'J':
            hdr->header = MAIL_SPAM_FLAG;
            hdr->data = tokens[0];
            break;
        case 'U':
            hdr->header = MAIL_OPTIONAL;
            break;
        case 'I':
            hdr->header = MAIL_MESSAGE_ID;
            hdr->data = tokens[0];
            break;
        case 'i':
            hdr->header = MAIL_REFERENCES;
            hdr->data = tokens[0];
            break;
        case 'j':
            hdr->header = MAIL_IN_REPLY_TO;
            hdr->data = tokens[0];
            break;
        default:
            return NULL;
        }

        hdr->next = NULL;
        if (prev) {
            prev->next = hdr;
        } else {
            first = hdr;
        }
        prev = hdr;
    }
    return first;
}


/* returns: 0 iff strings a and b are equal */
static int
StrNullCmp(const char *a, const char *b)
{
    return !a ? (NULL != b) : b && strcmp(a, b); 
}


static void
Participant(void *cbdata, MailHeaderName name, MailAddress *address)
{
    HeaderData **hdrp;
    HeaderData *hdr;

    hdrp = (HeaderData **) cbdata;
    hdr = *hdrp;

    if (!hdr) {
        fail("unexpected participant %s <%s>", address->displayName, address->address);
        return;
    }

    if (name != hdr->header) { 
        fail("unexpected participant header %d (%s <%s>), expected %d", name, address->displayName, address->address, hdr->header);
        abort();
    }

    if (VerboseMode) {
        fprintf (stderr, "participant [%s] (%s) <%s>\r\n",
                 MAIL_FROM == name ? "From" :
                 MAIL_SENDER == name ? "Sender" :
                 MAIL_REPLY_TO == name ? "Reply-to" :
                 MAIL_TO == name ? "To" :
                 MAIL_CC == name ? "Cc" :
                 MAIL_BCC == name ? "Bcc" :
                 MAIL_X_BEEN_THERE == name ? "X-BeenThere" :
                 MAIL_X_LOOP == name ? "X-Loop" :
                 "Unknown",
                 address->displayName,
                 address->address);
    }

    

    if(StrNullCmp(address->displayName, hdr->displayName)) {
        fail ("Expected display name \"%s\", got \"%s\".", 
              hdr->displayName, address->displayName);
    }

    if (StrNullCmp(address->address, hdr->address)) {
        fail ("Expected address \"%s\", got \"%s\".", 
              hdr->address, address->address);
    }


    
    *hdrp = hdr->next;
}


static void
Unstructured(void *cbdata, MailHeaderName name, const char *nameRaw, const char *data)
{
    HeaderData **hdrp;
    HeaderData *hdr;

    hdrp = (HeaderData **) cbdata;
    hdr = *hdrp;

    if (VerboseMode) {
        fprintf (stderr, "expecting: %d\r\n", hdr->header);
        fprintf (stderr, "unstructured: %s: %s\r\n", nameRaw, data);
    }

    if(!hdr) {
        fail("unexpected unstructured field: %s", nameRaw);
        return;
    }
    *hdrp = hdr->next;
}


static void
Date(void *cbdata, MailHeaderName name, uint64_t date)
{
    HeaderData **hdrp;
    HeaderData *hdr;

    hdrp = (HeaderData **) cbdata;
    hdr = *hdrp;

    if(!hdr) {
        fail("unexpected date: %d", name);
        return;
    }

    *hdrp = hdr->next;
}

static void
MessageId(void *cbdata, MailHeaderName name, const char *messageId)
{
    HeaderData **hdrp;
    HeaderData *hdr;

    hdrp = (HeaderData **) cbdata;
    hdr = *hdrp;

    if(!hdr) {
        fail("unexpected message id: %s", messageId);
        return;
    }

    if(name != hdr->header) {
        fail("unexpected header %d, expected %d", name, hdr->header);
    }

    if(StrNullCmp(messageId, hdr->data)) {
        fail ("Expected message id \"%s\", got \"%s\".", 
              hdr->data, messageId);
    }

    *hdrp = hdr->next;
}


START_TEST(testmailparser) 
{
    DIR *dir;
    struct dirent *ent;
    FILE *f;
    HeaderData *testcase;

    MailParser *parser;

    dir = opendir("mails");
    if (!dir) {
        fail("Couldn't open \"mails\" test case directory.");
    }
    while ((ent = readdir(dir))) {
        char filename[128];

        if (!strstr(ent->d_name, ".txt")) {
            continue;
        }

        if (strchr(ent->d_name, '~')) {
            /* don't test emacs backup files */
            continue;
        }

        snprintf(filename, 128, "mails/%s", ent->d_name);

        f = fopen(filename, "r");
        if (!f) {
            fail("Unable to open test file: %s", filename);
            break;
        }
        
        testcase = ParseTestCase(f);

        if (!testcase) {
            fail("Didn't understand test file: %s\n", filename);
            break;
        }

        printf ("Header parser testing file: %s\n", filename);
        parser = MailParserInit(f, (void *) &testcase);
        MailParserSetParticipantsCallback(parser, Participant);
        MailParserSetUnstructuredCallback(parser, Unstructured);
        MailParserSetDateCallback(parser, Date);
        MailParserSetMessageIdCallback(parser, MessageId);

        if(MailParseHeaders(parser)) {
            fail("Parser failed on: %s", ent->d_name);
        }
        MailParserDestroy(parser);
        fclose(f);

    }
}
END_TEST


