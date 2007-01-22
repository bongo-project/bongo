/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "RAMDirectory.h"

#include "Lock.h"
#include "Directory.h"
#include "FSDirectory.h"
#include "CLucene/util/VoidMap.h"
#include "CLucene/util/Misc.h"
#include "CLucene/debug/condition.h"

CL_NS_USE(util)
CL_NS_DEF(store)

  RAMFile::RAMFile()
  {
     length = 0;
     lastModified = Misc::currentTimeMillis();
  }
  RAMFile::~RAMFile(){
  }


  RAMDirectory::RAMLock::RAMLock(const char* name, RAMDirectory* dir):
    fname ( STRDUP_AtoA(name) ),
    directory(dir)
  {
  }
  RAMDirectory::RAMLock::~RAMLock()
  {
    _CLDELETE_LCaARRAY( fname );
  }
  TCHAR* RAMDirectory::RAMLock::toString(){
	  return STRDUP_TtoT(_T("LockFile@RAM"));
  }
  bool RAMDirectory::RAMLock::isLocked() {
   return directory->fileExists(fname);
  }
  bool RAMDirectory::RAMLock::obtain(){
    SCOPED_LOCK_MUTEX(directory->files_mutex);
    if (!directory->fileExists(fname)) {
        IndexOutput* tmp = directory->createOutput(fname);
        tmp->close();
        _CLDELETE(tmp);

      return true;
    }
    return false;
  }

  void RAMDirectory::RAMLock::release(){
    directory->deleteFile(fname);
  }


  RAMIndexOutput::~RAMIndexOutput(){
     if ( deleteFile )
        _CLDELETE(file);
  }
  RAMIndexOutput::RAMIndexOutput(RAMFile* f):file(f) {
    pointer = 0;
    deleteFile = false;
  }
  
  RAMIndexOutput::RAMIndexOutput():
     file(_CLNEW RAMFile)
  {
     pointer = 0;
     deleteFile = true;
  }

  void RAMIndexOutput::writeTo(IndexOutput* out){
    flush();
    int64_t end = file->length;
    int64_t pos = 0;
    int32_t buffer = 0;
    while (pos < end) {
      int32_t length = LUCENE_STREAM_BUFFER_SIZE;
      int64_t nextPos = pos + length;
      if (nextPos > end) {                        // at the last buffer
        length = (int32_t)(end - pos);
      }
      out->writeBytes((uint8_t*)file->buffers[buffer++], length);
      pos = nextPos;
    }
  }

  void RAMIndexOutput::reset(){
	seek(0);
    file->length = 0;
  }

  void RAMIndexOutput::flushBuffer(const uint8_t* src, const int32_t len) {
    uint8_t* buffer = NULL;
    int32_t bufferPos = 0;
    while (bufferPos != len) {
	    uint32_t bufferNumber = pointer/LUCENE_STREAM_BUFFER_SIZE;
	    int32_t bufferOffset = pointer%LUCENE_STREAM_BUFFER_SIZE;
	    int32_t bytesInBuffer = LUCENE_STREAM_BUFFER_SIZE - bufferOffset;
	    int32_t remainInSrcBuffer = len - bufferPos;
      	int32_t bytesToCopy = bytesInBuffer >= remainInSrcBuffer ? remainInSrcBuffer : bytesInBuffer;
	
		if (bufferNumber == file->buffers.size()){
		  buffer = _CL_NEWARRAY(uint8_t, LUCENE_STREAM_BUFFER_SIZE);
	      file->buffers.push_back( buffer );
		}else{
		  buffer = file->buffers[bufferNumber];	
		}
		memcpy(buffer+bufferOffset, src+bufferPos, bytesToCopy * sizeof(uint8_t));
		bufferPos += bytesToCopy;
        pointer += bytesToCopy;
	}
    if (pointer > file->length)
      file->length = pointer;

    file->lastModified = Misc::currentTimeMillis();
  }

  void RAMIndexOutput::close() {
    IndexOutput::close();
  }

  /** Random-at methods */
  void RAMIndexOutput::seek(const int64_t pos){
    IndexOutput::seek(pos);
    pointer = (int32_t)pos;
  }
  int64_t RAMIndexOutput::Length() {
    return file->length;
  }


  RAMIndexInput::RAMIndexInput(RAMFile* f):file(f) {
    pointer = 0;
    _length = f->length;
  }
  RAMIndexInput::RAMIndexInput(const RAMIndexInput& clone):
    file(clone.file),
    BufferedIndexInput(clone)
  {
    pointer = clone.pointer;
    _length = clone._length;
  }
  RAMIndexInput::~RAMIndexInput(){
      close();
  }
  IndexInput* RAMIndexInput::clone() const
  {
    RAMIndexInput* ret = _CLNEW RAMIndexInput(*this);
    return ret;
  }
  int64_t RAMIndexInput::length() {
    return _length;
  }
  void RAMIndexInput::readInternal(uint8_t* dest, int32_t _destOffset, const int32_t len) {
    const int64_t bytesAvailable = file->length - pointer;
    int64_t remainder = len <= bytesAvailable ? len : bytesAvailable;
    int32_t start = pointer;
    int32_t destOffset = _destOffset;
    while (remainder != 0) {
      int32_t bufferNumber = start / LUCENE_STREAM_BUFFER_SIZE;
      int32_t bufferOffset = start % LUCENE_STREAM_BUFFER_SIZE;
      int32_t bytesInBuffer = LUCENE_STREAM_BUFFER_SIZE - bufferOffset;

	  /* The buffer's entire length (bufferLength) is defined by IndexInput.h
      ** as int32_t, so obviously the number of bytes in a given segment of the
      ** buffer won't exceed the the capacity of int32_t.  Therefore, the
      ** int64_t->int32_t cast on the next line is safe. */
      int32_t bytesToCopy = bytesInBuffer >= remainder ? static_cast<int32_t>(remainder) : bytesInBuffer;
      uint8_t* buffer = (uint8_t*)file->buffers[bufferNumber];

	  memcpy(dest+destOffset,buffer+bufferOffset,bytesToCopy * sizeof(uint8_t));

      destOffset += bytesToCopy;
      start += bytesToCopy;
      remainder -= bytesToCopy;
      pointer += bytesToCopy;
    }
  }

  void RAMIndexInput::close() {
    BufferedIndexInput::close();
  }

  void RAMIndexInput::seekInternal(const int64_t pos) {
	  CND_PRECONDITION(pos>=0 &&pos<this->_length,"Seeking out of range")
    pointer = (int32_t)pos;
  }






  char** RAMDirectory::list() const{
    SCOPED_LOCK_MUTEX(files_mutex);
    int32_t size = 0;
    char** list = _CL_NEWARRAY(char*,files.size()+1);

	FileMap::const_iterator itr = files.begin();
    while (itr != files.end()){
	  list[size] = lucenestrdup((char*)itr->first CL_FILELINE);
      itr++;
      size++;
    }
    list[size]=NULL;
    return list;
  }

  RAMDirectory::RAMDirectory():
   Directory(),files(true,true)
  {
  }
  
  RAMDirectory::~RAMDirectory(){
   //todo: should call close directory?
  }

  void RAMDirectory::_copyFromDir(Directory* dir, bool closeDir)
  {
    char** files = dir->list();
    int i=0;
    uint8_t* buf = _CL_NEWARRAY(uint8_t, LUCENE_STREAM_BUFFER_SIZE);
    
    while ( files[i] != NULL ){
      // make place on ram disk
      IndexOutput* os = createOutput(files[i]);
      // read current file
      IndexInput* is = dir->openInput(files[i]);
      // and copy to ram disk
      //todo: this could be a problem when copying from big indexes... 
      int64_t len = is->length();
      int64_t readCount = 0;
      while (readCount < len) {
		int32_t toRead = (int32_t)(readCount + LUCENE_STREAM_BUFFER_SIZE > len ? len - readCount : LUCENE_STREAM_BUFFER_SIZE);
	    is->readBytes(buf, 0, toRead);
	    os->writeBytes(buf, toRead);
        readCount += toRead;
      }

      // graceful cleanup
      is->close();
      _CLDELETE(is);
      os->close();
      _CLDELETE(os);

      _CLDELETE_LCaARRAY(files[i]);
      i++;
    }
    _CLDELETE_ARRAY(buf);
    _CLDELETE_ARRAY(files);

    if (closeDir)
       dir->close();
  }
  RAMDirectory::RAMDirectory(Directory* dir):
   Directory(),files(true,true)
  {
    _copyFromDir(dir,false);
    
  }
  
   RAMDirectory::RAMDirectory(const char* dir):
      Directory(),files(true,true)
   {
      Directory* fsdir = FSDirectory::getDirectory(dir,false);
      try{
         _copyFromDir(fsdir,false);
      }_CLFINALLY(fsdir->close());

   }

  bool RAMDirectory::fileExists(const char* name) const {
    SCOPED_LOCK_MUTEX(files_mutex);
    return files.exists(name);
  }

  int64_t RAMDirectory::fileModified(const char* name) const {
	  SCOPED_LOCK_MUTEX(files_mutex);
	  const RAMFile* f = files.get(name);
	  return f->lastModified;
  }

  int64_t RAMDirectory::fileLength(const char* name) const{
	  SCOPED_LOCK_MUTEX(files_mutex);
	  RAMFile* f = files.get(name);
      return f->length;
  }


  IndexInput* RAMDirectory::openInput(const char* name) {
    SCOPED_LOCK_MUTEX(files_mutex);
    RAMFile* file = files.get(name);
    if (file == NULL) { /* DSR:PROPOSED: Better error checking. */
      _CLTHROWA(CL_ERR_IO,"[RAMDirectory::open] The requested file does not exist.");
    }
    return _CLNEW RAMIndexInput( file );
  }

  void RAMDirectory::close(){
      SCOPED_LOCK_MUTEX(files_mutex);
      files.clear();
  }

  void RAMDirectory::deleteFile(const char* name, const bool throwError) {
    SCOPED_LOCK_MUTEX(files_mutex);
    files.remove(name);
  }

  void RAMDirectory::renameFile(const char* from, const char* to) {
	SCOPED_LOCK_MUTEX(files_mutex);
	FileMap::iterator itr = files.find(from);

    /* DSR:CL_BUG_LEAK:
    ** If a file named $to already existed, its old value was leaked.
    ** My inclination would be to prevent this implicit deletion with an
    ** exception, but it happens routinely in CLucene's internals (e.g., during
    ** IndexWriter.addIndexes with the file named 'segments'). */
    if (files.exists(to)) {
      files.remove(to);
    }
	if ( itr == files.end() ){
		char tmp[1024];
		_snprintf(tmp,1024,"cannot rename %s, file does not exist");
		_CLTHROWT(CL_ERR_IO,tmp);
	}
	CND_PRECONDITION(itr != files.end(), "itr==files.end()")
	RAMFile* file = itr->second;
    files.removeitr(itr,false,true);
    files.put(STRDUP_AtoA(to), file);
  }

  
  void RAMDirectory::touchFile(const char* name) {
    RAMFile* file = NULL;
    {
      SCOPED_LOCK_MUTEX(files_mutex);
      file = files.get(name);
	}
    uint64_t ts1 = file->lastModified;
    uint64_t ts2 = Misc::currentTimeMillis();

	//make sure that the time has actually changed
    while ( ts1==ts2 ) {
        _sleep(1);
        ts2 = Misc::currentTimeMillis();
    };

    file->lastModified = ts2;
  }

  IndexOutput* RAMDirectory::createOutput(const char* name) {
    /* Check the $files VoidMap to see if there was a previous file named
    ** $name.  If so, delete the old RAMFile object, but reuse the existing
    ** char buffer ($n) that holds the filename.  If not, duplicate the
    ** supplied filename buffer ($name) and pass ownership of that memory ($n)
    ** to $files. */

    SCOPED_LOCK_MUTEX(files_mutex);

    const char* n = files.getKey(name);
    if (n != NULL) {
	   RAMFile* rf = files.get(name);
      _CLDELETE(rf);
    } else {
      n = STRDUP_AtoA(name);
    }

    RAMFile* file = _CLNEW RAMFile();
    #ifdef _DEBUG
      file->filename = n;
    #endif
    files[n] = file;

    IndexOutput* ret = _CLNEW RAMIndexOutput(file);
    return ret;
  }

  LuceneLock* RAMDirectory::makeLock(const char* name) {
    return _CLNEW RAMLock(name,this);
  }

  TCHAR* RAMDirectory::toString() const{
	return STRDUP_TtoT( _T("RAMDirectory") );
  }
CL_NS_END
