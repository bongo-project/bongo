#include <glib.h>
#include <bongoutil.h>

/* TODO: the old function had a char *.  should this be more generic somehow? */
int
GArrayFindUnsorted(GArray *array, void *needle, ArrayCompareFunc compare)
{
    unsigned int i;

    if (!array || !compare) {
        return -1;
    }

    for (i = 0; i < array->len; i++) {
        if (!compare(needle, g_array_index(array, char *, i))) {
            return (int)i;
        }
    }
    
    return -1;
}

/* TODO: the old function had a char *.  should this be more generic somehow? */
int
GArrayFindSorted(GArray *array, void *needle, ArrayCompareFunc compare)
{
    void *data;

    if (!array || !compare) {
        return -1;
    }

    data = bsearch(needle, array->data, array->len, sizeof(char *), compare);

    if (data) {
        return ((char*)data - (char*)array->data) / sizeof(char *);
    } else {
        return -1;
    }
}
