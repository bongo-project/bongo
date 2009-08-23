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

int
GArrayFindSorted(GArray *array, void *needle, ssize_t type_size, ArrayCompareFunc compare)
{
    void *data;

    if (!array || !compare) {
        return -1;
    }

    data = bsearch(needle, array->data, array->len, type_size, compare);

    if (data) {
        return ((void *)data - (void *)array->data) / type_size;
    } else {
        return -1;
    }
}
