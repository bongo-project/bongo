#ifndef STREAMIO_READER_H
#define STREAMIO_READER_H

#include <bongostream.h>
#include "lucene-index.h"

namespace jstreams {

/* FIXME: this class should be based directly on StreamBase, since
   BongoStream is already buffered.  
*/

template <class T>
class BongoStreamInputStream : public BufferedInputStream <T>
{
private:
    FILE *fh;
    BongoStream *bongostream;

    uint64_t startOffset;
    uint64_t streamLength;

    char *encoding;
    char *charset;
    
    bool eos;
    bool started;

protected:

    int32_t fillBuffer(T* start, int32_t space);

public:
    BongoStreamInputStream(FILE *f, int64_t offset, int64_t length, 
                          char *encoding, char *charset);
    ~BongoStreamInputStream();
};


/* constructor */
template <class T>
BongoStreamInputStream<T>::BongoStreamInputStream (FILE *f, 
                                                 int64_t offset, int64_t length, 
                                                 char *encoding, char *charset)
    : bongostream(BongoStreamCreate(NULL, 0, FALSE)),
      eos(FALSE),
      fh(f),
      startOffset(offset),
      streamLength(length),
      started(FALSE),
      encoding(NULL),
      charset(NULL)
{
    if (encoding) {
        this->encoding = MemStrdup(encoding);
        BongoStreamAddCodec(bongostream, encoding, FALSE);
    }
    if (charset) {
        this->charset = MemStrdup(charset);
        BongoStreamAddCodec(bongostream, charset, FALSE);
    }
    
    if (BongoStreamAddCodec(bongostream, "UCS-4", TRUE)) {
        printf("bongostore: couldn't add ucs-4 encoder!\n");
    }

}


/* destructor */
template <class T>
BongoStreamInputStream<T>::~BongoStreamInputStream()
{
    if (bongostream) {
        BongoStreamFree(bongostream);
    }
    if (charset) {
        MemFree(charset);
    }
    if (encoding) {
        MemFree(encoding);
    }
}


template <class T>
int32_t 
BongoStreamInputStream<T>::fillBuffer(T* start, int32_t space)
{
    char buf[1024];
    int32_t numread;
    int32_t ret;

    if (!started) {
        int pos = fseek(fh, startOffset, SEEK_SET);

        assert(ftell(fh) == startOffset);
        started = TRUE;
    }

    ret = BongoStreamGet(bongostream, (char *) start, space * sizeof(T));

    if (!ret && !eos) {
        numread = fread(buf, 1, sizeof(buf), fh);
        if (numread <= 0) {
            if (feof(fh)) {
                eos = TRUE;
                BongoStreamEos(bongostream);
            }
            if (ferror(fh)) {
                this->status = Error;
                this->error = "Error reading file.";
            }
            fh = NULL;
        } else {
            BongoStreamPut(bongostream, buf, numread);
        }

        ret = BongoStreamGet(bongostream, (char *) start, space * sizeof(T));
    }
    
    return ret ? ret / sizeof(T) : eos ? -1 : 0;
}


}



#endif
