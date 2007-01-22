#include <config.h>
#include <xpl.h>
#include <math.h>
#include "streamio-reader.h"
#include "file-reader.h"

#include <assert.h>

FileReader::FileReader(FILE *f, int64_t offset, int64_t length) : stream(NULL),
                                                                          availOffset(0),
                                                                          availLength(0),
                                                                          eos(FALSE),
                                                                          fh(f),
                                                                          startOffset(offset),
                                                                          streamLength(length),
                                                                          started(FALSE)
{
}

FileReader::~FileReader()
{
}

TCHAR 
FileReader::readChar()
{
    TCHAR tc;
    if (position == streamLength) {
        return 0;
    }

    char c = fgetc(fh);
    
    num = lucene_utf8towc(&c, &tc, 1);

    return tc;
}

void
FileReader::Start()
{
    if (!started) {
        int pos = fseek(fh, startOffset, SEEK_SET);
        
        assert(ftell(fh) == startOffset);
        
        started = TRUE;
    }
}


void
FileReader::FillBuffer()
{
    Start();

    char buf[1024];
    int numRead;

    int toRead = available();
    
    if (toRead > 1024) {
        toRead = 1024;
    }
    
    if (toRead != 0) {
        numRead = fread(buf, 1, toRead, fh);
    } else {
        numRead = 0;
    }
    
    if (numRead == 0) {
        eos = TRUE;
        BongoStreamEos(stream);
        return;
    }

    BongoStreamPut(stream, buf, numRead);
}

bool
FileReader::RefreshBuffer() 
{
    EnsureRaw();
    
    if (availLength > 0) {
        return TRUE;
    }
    
    availOffset = 0;
    
    availLength = BongoStreamGet(stream, availBuffer, AVAILBUFFER_SIZE);
    while (availLength == 0 && !eos) {
        FillBuffer();
        availLength = BongoStreamGet(stream, availBuffer, AVAILBUFFER_SIZE);
    }

    return (availLength > 0);
}

static int CopyData(TCHAR *to, int toOffset, char *from, int fromOffset, int count) 
{
    int src = 0;
    int dest = 0;
    
    while (src < count) {
        src += lucene_utf8towc(&to[toOffset + dest], &from[fromOffset + src], 2);
        dest++;
    }

    return dest;
}


int32_t
FileReader::read(TCHAR* b, const int64_t offset, const int32_t count)
{
    if (!RefreshBuffer()) {
        return 0;
    }
    
    int toRead;
    
    if (count > availLength) {
        toRead = availLength;
    } else {
        toRead = count;
    }

    int bytesRead = CopyData(b, offset, availBuffer, availOffset, toRead);

    availLength -= toRead;
    availOffset += toRead;

    return bytesRead;
}

TCHAR 
FileReader::peek()
{
    
}

void
FileReader::EnsureRaw() 
{
    if (stream == NULL) {
        stream = BongoStreamCreate(NULL, 0, FALSE);
    }
}

int64_t
FileReader::available()
{
    return streamLength - position();
}

void 
FileReader::close()
{
}

int64_t
FileReader::position()
{
    return ftell(fh) - startOffset;
}

void
FileReader::seek(int64_t position)
{
    fseek(fh, startOffset + position, SEEK_SET);
}
