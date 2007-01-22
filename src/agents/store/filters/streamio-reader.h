#ifndef STREAMIO_READER_H
#define STREAMIO_READER_H

#include <bongostream.h>
#include "lucene-index.h"

#define AVAILBUFFER_SIZE 1024

class StreamIOReader : public Reader 
{
private :
    FILE *fh;
    BongoStream *stream;

    uint64_t startOffset;
    uint64_t streamLength;

    char *encoding;
    char *charset;
    
    char availBuffer[AVAILBUFFER_SIZE];
    int availOffset;
    int availLength;

    bool eos;
    bool started;

    void Start();

    void FillBuffer();
    bool RefreshBuffer();
    void EnsureRaw();

public :
    StreamIOReader(FILE *fh, int64_t offset, int64_t length, char *encoding, char *charset);
    ~StreamIOReader();

    TCHAR readChar();
    int32_t read(TCHAR* b, const int64_t start, const int32_t length);
    TCHAR peek();
    int64_t available();
    void close();
    int64_t position();
    void seek(int64_t position);
};

#endif
