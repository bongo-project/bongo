#include <config.h>
#include <bongocal.h>

#include <stdio.h>
#include <ical.h>
#include <xpl.h>
#include <bongoutil.h>
#include <bongojson.h>
#include <bongocal.h>
#include <bongo-cal-private.h>

static char *
ReadStream(char *s, size_t size, void *d)
{
    char *c = fgets(s, size, (FILE*)d);
    
    return c;
}

static BongoJsonObject *
ConvertFile(const char *filename)
{
    icalparser *parser;
    FILE *f;
    icalcomponent *component;    
    BongoJsonObject *obj;

    parser = icalparser_new();
    f = fopen(filename, "r");

    if (!f) {
        printf("couldn't open input file %s\n", filename);
        return NULL;
    }
    
    icalparser_set_gen_data(parser, f);

    component = icalparser_parse(parser, ReadStream);
    icalcomponent_strip_errors(component);

    obj = BongoCalIcalToJson(component);

    return obj;
}

static char *
GetFilename(const char *tzid)
{
    int len;
    char *ret;
    int i;
    char *dest;

    len = strlen(tzid);

    assert(strncmp(tzid, "/bongo/", strlen("/bongo/")) == 0);
    
    ret = MemMalloc(len * 3 + 4 + 1);

    dest = ret;

    i = strlen("/bongo/");

    for (; i < len; i++) {
        if (tzid[i] == '/') {
            *dest++ = '-';
        } else {
            *dest++ = tzid[i];
        }
    }
    strcpy(dest, ".zone");

    return ret;
}

static void
ImportFile(const char *filename, char *destdir, FILE *tznames)
{
    BongoCalObject *cal;
    BongoJsonObject *obj = NULL;
    BongoHashtable *tzs = NULL;
    BongoHashtableIter iter;
    
    obj = ConvertFile(filename);
    if (!obj) {
        return;
    }

    cal = BongoCalObjectNew(obj);
    if (!cal) {
        return;
    }
    
    tzs = BongoCalObjectGetTimezones(cal);
    if (!tzs) {
        fprintf(stderr, "Couldn't get timezones from ical file %s\n", filename);        
        return;
    }
    
    for (BongoHashtableIterFirst(tzs, &iter);
         iter.key != NULL;
         BongoHashtableIterNext(tzs, &iter)) {
        const char *tzid = iter.key;
        char path[PATH_MAX];
        FILE *f;
        char *tz;
        char *name;
        char *p;

        snprintf(path, PATH_MAX, "%s/%s", destdir, GetFilename(tzid));

        f = fopen(path, "w");
        
        fprintf(f, "{ \"version\": { \"value\": \"2.0\" }, \"components\":[");
        
        tz = BongoJsonObjectToString(((BongoCalTimezone*)iter.value)->json);
        
        fwrite(tz, 1, strlen(tz), f);

        fprintf(f, "] }");

        name = MemStrdup(strrchr(tzid, '/') + 1);
        for (p = name; *p != '\0'; p++) {
            if (*p == '_') {
                *p = ' ';
            }
        }   

        fprintf(tznames, "%s|%s\n", name, tzid);
        
        fclose(f);
    }
}

int
main(int argc, char *argv[]) 
{
    int i;
    char *destdir;
    char path[PATH_MAX];
    FILE *tznames;

    MemoryManagerOpen("bongo-import-tz");

    if (argc < 3) {
        printf("usage:\n\tbongo-import-tz icsfiles... outputdir\n");
        return 1;
    }
    
    destdir = argv[argc - 1];
    
    snprintf(path, PATH_MAX, "%s/%s", destdir, "generated.tznames");
    tznames = fopen(path, "a");

    for (i = 1; i < argc - 1; i++) {
        ImportFile(argv[i], destdir, tznames);
    }

    fclose(tznames);

    return 0;
}
