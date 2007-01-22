#ifndef STREAMIO_READER_H
#define STREAMIO_READER_H

#include "lucene-index.h"

class FileReader : public Reader 
{
private :
    FILE *fh;

    uint64_t startOffset;
    uint64_t streamLength;

public :
    FileReader(FILE *fh, int64_t offset, int64_t length);
    ~FileReader();

    TCHAR readChar();
    int32_t read(TCHAR* b, const int64_t start, const int32_t length);
    TCHAR peek();
    int64_t available();
    void close();
    int64_t position();
    void seek(int64_t position);
};

#endif
