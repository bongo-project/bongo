/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "AnalysisHeader.h"

CL_NS_USE(util)
CL_NS_DEF(analysis)

const TCHAR* Token::defaultType=_T("word");

Token::Token():
	_startOffset (0),
	_endOffset (0),
	_type ( defaultType ),
	positionIncrement (1)
{
    termTextLen = 0;
#ifndef LUCENE_TOKEN_WORD_LENGTH
    _termText = NULL;
	bufferTextLen = 0;
#else
    _termText[0] = 0; //make sure null terminated
	bufferTextLen = LUCENE_TOKEN_WORD_LENGTH+1;
#endif
}

Token::~Token(){
#ifndef LUCENE_TOKEN_WORD_LENGTH
    _CLDELETE_CARRAY(_termText);
#endif
}

Token::Token(const TCHAR* text, const int32_t start, const int32_t end, const TCHAR* typ):
	_startOffset (start),
	_endOffset (end),
	_type ( typ ),
	positionIncrement (1)
{
    termTextLen = 0;
#ifndef LUCENE_TOKEN_WORD_LENGTH
    _termText = NULL;
	bufferTextLen = 0;
#else
    _termText[0] = 0; //make sure null terminated
	bufferTextLen = LUCENE_TOKEN_WORD_LENGTH+1;
#endif
	setText(text);
}

void Token::set(const TCHAR* text, const int32_t start, const int32_t end, const TCHAR* typ){
	_startOffset = start;
	_endOffset   = end;
	_type        = typ;
	positionIncrement = 1;
	setText(text);
	
}

void Token::setText(const TCHAR* text){
	int32_t oldlen = termTextLen;
	termTextLen = _tcslen(text);
	
#ifndef LUCENE_TOKEN_WORD_LENGTH
	if ( termTextLen > oldlen || _termText == NULL ){
		_CLDELETE_CARRAY(_termText);
		_termText = _CL_NEWARRAY(TCHAR,termTextLen+1);
		bufferTextLen = termTextLen+1;
	}
	_tcsncpy(_termText,text,termTextLen+1);
#else
	if ( termTextLen > LUCENE_TOKEN_WORD_LENGTH ){
    	//in the case where this occurs, we will leave the endOffset as it is
    	//since the actual word still occupies that space.
		termTextLen=LUCENE_TOKEN_WORD_LENGTH;
	}
	_tcsncpy(_termText,text,termTextLen+1);
#endif
	_termText[termTextLen] = 0; //make sure null terminated
}

void Token::growBuffer(size_t size){
	if(bufferTextLen>size)
		return;
#ifndef LUCENE_TOKEN_WORD_LENGTH
	_CLDELETE_CARRAY(_termText);
	termTextLen=-1;
	bufferTextLen = size+1;
	_termText = _CL_NEWARRAY(TCHAR,bufferTextLen);
#else
	_CLTHROWA(CL_ERR_TokenMgr,"Couldn't grow Token buffer");
#endif
}

void Token::setPositionIncrement(int32_t posIncr) {
	if (posIncr < 0) {
		_CLTHROWA(CL_ERR_IllegalArgument,"positionIncrement must be >= 0");
	}
	positionIncrement = posIncr;
}

int32_t Token::getPositionIncrement() { return positionIncrement; }

// Returns the Token's term text. 
const TCHAR* Token::termText() {
	return (const TCHAR*) _termText; 
}
size_t Token::termTextLength() { 
	if ( termTextLen == -1 ) //it was invalidated by growBuffer
		termTextLen = _tcslen(_termText);
	return termTextLen; 
}
void Token::resetTermTextLen(){
	termTextLen=-1;
}
bool Token::OrderCompare::operator()( Token* t1, Token* t2 ) const{
	if(t1->startOffset()>t2->startOffset())
        return false;
    if(t1->startOffset()<t2->startOffset())
        return true;
	return true;
}


Token* TokenStream::next(){
	Token* t = _CLNEW Token;
	if ( !next(t) )
		_CLDELETE(t);
	return t;
}


TokenFilter::TokenFilter(TokenStream* in, bool deleteTS):
	input(in),
	deleteTokenStream(deleteTS)
{
}
TokenFilter::~TokenFilter(){
	close();
}

// Close the input TokenStream.
void TokenFilter::close() {
    if ( input != NULL ){
		input->close();
        if ( deleteTokenStream )
			_CLDELETE( input );
    }
    input = NULL;
}



Tokenizer::Tokenizer() {
	input = NULL;
}

Tokenizer::Tokenizer(CL_NS(util)::Reader* _input):
    input(_input)
{
}

void Tokenizer::close(){
	if (input != NULL) {
		// ? delete input;
		input = NULL;
	}
}

Tokenizer::~Tokenizer(){ 
    close();
}
CL_NS_END
