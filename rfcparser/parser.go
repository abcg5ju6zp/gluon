package rfcparser

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const DefaultContinuationMessage = "Ready"

// MaxLiteralSize is the maximum accepted literal size (70 MiB). A literal declaring this size
// or more is rejected.
const MaxLiteralSize = 70 * 1024 * 1024

// ErrLiteralTooLarge is wrapped by the error returned when a literal declares more than
// MaxLiteralSize bytes.
var ErrLiteralTooLarge = fmt.Errorf("literal size exceeds maximum size of 70MB")

// Parser provide facilities to consumes tokens from a given scanner. Advance should be called at least once before
// any checks in order to initialize the previousToken.
type Parser struct {
	scanner               *Scanner
	literalContinuationCb func(message string) error
	previousToken         Token
	currentToken          Token
}

type Error struct {
	Token   Token
	Message string
}

type Bytes struct {
	Value  []byte
	Offset int
}

func (c Bytes) IntoString() String {
	return String{
		Value:  string(c.Value),
		Offset: c.Offset,
	}
}

type String struct {
	Value  string
	Offset int
}

func (c String) ToLower() String {
	return String{
		Value:  strings.ToLower(c.Value),
		Offset: c.Offset,
	}
}

func (p *Error) Error() string {
	return fmt.Sprintf("[Error offset=%v]: %v", p.Token.Offset, p.Message)
}

func (p *Error) IsEOF() bool {
	return p.Token.TType == TokenTypeEOF
}

func IsError(err error) bool {
	var perr *Error
	return errors.As(err, &perr)
}

type ParserState struct {
	prevToken Token
	curToken  Token
}

func NewParser(s *Scanner) *Parser {
	return &Parser{scanner: s}
}

func NewParserWithLiteralContinuationCb(s *Scanner, f func(string) error) *Parser {
	return &Parser{
		scanner: s, literalContinuationCb: f,
		previousToken: Token{
			TType:  TokenTypeEOF,
			Value:  0,
			Offset: 0,
		},
		currentToken: Token{
			TType:  TokenTypeEOF,
			Value:  0,
			Offset: 0,
		},
	}
}

// ParseAString parses an astring according to RFC3501.
func (p *Parser) ParseAString() (String, error) {
	/*
		astring         = 1*ASTRING-CHAR / string
	*/
	if p.Check(TokenTypeDQuote) || p.Check(TokenTypeLCurly) {
		return p.ParseString()
	}

	astring, err := p.CollectBytesWhileMatchesWith(IsAStringChar)
	if err != nil {
		return String{}, err
	}

	return astring.IntoString(), nil
}

func (p *Parser) TryParseString() (String, bool, error) {
	if !p.Check(TokenTypeDQuote) && !p.Check(TokenTypeLCurly) {
		return String{}, false, nil
	}

	v, err := p.ParseString()

	return v, true, err
}

// ParseString parses a string according to RFC3501.
func (p *Parser) ParseString() (String, error) {
	/*
		string          = quoted / literal
	*/
	if p.Check(TokenTypeDQuote) {
		return p.ParseQuoted()
	} else if p.Check(TokenTypeLCurly) {
		startOffset := p.currentToken.Offset

		l, err := p.ParseLiteral()
		if err != nil {
			return String{}, err
		}

		return String{Value: string(l), Offset: startOffset}, err
	}

	return String{}, p.MakeError("unexpected character, expected start of quote or literal")
}

// ParseQuoted parses a quoted string.
func (p *Parser) ParseQuoted() (String, error) {
	/*
		quoted          = DQUOTE *QUOTED-CHAR DQUOTE

		QUOTED-CHAR     = <any TEXT-CHAR except quoted-specials> /
		                  "\" quoted-specials
	*/
	if err := p.Consume(TokenTypeDQuote, `Expected '"' for quoted start`); err != nil {
		return String{}, err
	}

	var quoted []byte

	startOffset := p.currentToken.Offset

	for {
		if ok, err := p.MatchesWith(IsQuotedChar); err != nil {
			return String{}, err
		} else if ok {
			quoted = append(quoted, p.previousToken.Value)
		} else {
			if ok, err := p.Matches(TokenTypeBackslash); err != nil {
				return String{}, err
			} else if ok {
				if err := p.ConsumeWith(IsQuotedSpecial, `Expected '\' or '"' after '\' in quoted`); err != nil {
					return String{}, err
				}
				quoted = append(quoted, p.previousToken.Value)
			} else {
				break
			}
		}
	}

	if err := p.Consume(TokenTypeDQuote, `Expected '"' for quoted end`); err != nil {
		return String{}, err
	}

	return String{Value: string(quoted), Offset: startOffset}, nil
}

// ParseLiteral parses a literal as defined in RFC3501 and additionally accepts the
// non-synchronizing literal form of RFC 7888 ("{<n>+}"), in which case no continuation is
// sent. The full literal is buffered and returned.
func (p *Parser) ParseLiteral() ([]byte, error) {
	/*
		literal         = "{" number ["+"] "}" CRLF *CHAR8
	*/
	literalSize, nonSynchronous, err := p.ParseLiteralHeader()
	if err != nil {
		return nil, err
	}

	if err := p.ConsumeLiteralLF(nonSynchronous); err != nil {
		return nil, err
	}

	literal := make([]byte, literalSize)

	if err := p.scanner.ConsumeBytes(literal); err != nil {
		return nil, err
	}

	// Need to advance parser after scanning literal so that next token is loaded
	if err := p.Advance(); err != nil {
		return nil, err
	}

	return literal, nil
}

// ParseLiteralHeader parses the literal header "{" number ["+"] "}" CR. It performs the
// declared-size validation (<=0 and >= MaxLiteralSize). It consumes neither the LF following
// CR nor any literal data. Use ConsumeLiteralLF to consume the LF (which also fires the
// continuation callback for synchronous literals), then StreamLiteralBody or ConsumeBytes to
// read the declared number of bytes.
func (p *Parser) ParseLiteralHeader() (int, bool, error) {
	if err := p.Consume(TokenTypeLCurly, "expected '{' for literal start"); err != nil {
		return 0, false, err
	}

	literalSize, err := p.ParseNumber()
	if err != nil {
		return 0, false, err
	}

	if literalSize <= 0 {
		return 0, false, fmt.Errorf("invalid literal size")
	}

	if literalSize >= MaxLiteralSize {
		return 0, false, fmt.Errorf("%w (declared %d)", ErrLiteralTooLarge, literalSize)
	}

	// RFC 7888 non-synchronizing literal: "{<n>+}".
	nonSynchronous := false

	if p.Check(TokenTypePlus) {
		if err := p.Consume(TokenTypePlus, "expected '+' after literal size"); err != nil {
			return 0, false, err
		}

		nonSynchronous = true
	}

	if err := p.Consume(TokenTypeRCurly, "expected '}' for literal end"); err != nil {
		return 0, false, err
	}

	if err := p.Consume(TokenTypeCR, "expected CR"); err != nil {
		return 0, false, err
	}

	return literalSize, nonSynchronous, nil
}

// ConsumeLiteralLF consumes the LF after a literal header CR. For synchronous literals the
// continuation callback is invoked at exactly the same point as in ParseLiteral (after CR,
// before LF). Non-synchronizing literals never trigger the callback.
func (p *Parser) ConsumeLiteralLF(nonSynchronous bool) error {
	// Call literal continuation callback here or we risk getting stuck forever trying to read the next token
	// in the scanner due to the byte buffers implementation as there will be no more new input until the we signal
	// for more input.
	if !nonSynchronous && p.Check(TokenTypeLF) && p.literalContinuationCb != nil {
		if err := p.literalContinuationCb(DefaultContinuationMessage); err != nil {
			return fmt.Errorf("error occurred during literal continuation callback:%w", err)
		}
	}

	if err := p.Consume(TokenTypeLF, "expected LF after CR"); err != nil {
		return err
	}

	return nil
}

// StreamLiteralBody streams exactly size literal bytes into dst without buffering them, then
// advances the parser so the next token (the trailing CR) is loaded. The first literal byte is
// already held by the scanner (see Scanner.StreamLiteralBody).
func (p *Parser) StreamLiteralBody(dst io.Writer, size int) error {
	if err := p.scanner.StreamLiteralBody(dst, size); err != nil {
		return err
	}

	// Need to advance parser after scanning literal so that next token is loaded
	return p.Advance()
}

// ReadLiteralBody reads and buffers exactly size literal bytes after the literal header and
// CR/LF have already been consumed with ParseLiteralHeader and ConsumeLiteralLF. It is the
// buffered counterpart of StreamLiteralBody.
func (p *Parser) ReadLiteralBody(size int) ([]byte, error) {
	literal := make([]byte, size)

	if err := p.scanner.ConsumeBytes(literal); err != nil {
		return nil, err
	}

	// Need to advance parser after scanning literal so that next token is loaded
	if err := p.Advance(); err != nil {
		return nil, err
	}

	return literal, nil
}

func (p *Parser) ParseStringAfterContinuation(continuationMessage string) (String, error) {
	if err := p.Consume(TokenTypeCR, "expected CR"); err != nil {
		return String{}, err
	}

	if p.Check(TokenTypeLF) && p.literalContinuationCb != nil {
		if err := p.literalContinuationCb(continuationMessage); err != nil {
			return String{}, fmt.Errorf("error occurred during literal continuation callback:%w", err)
		}
	}

	if err := p.Consume(TokenTypeLF, "expected LF after CR"); err != nil {
		return String{}, err
	}

	return p.ParseAString()
}

// ParseNumber parses a non decimal number without any signs.
func (p *Parser) ParseNumber() (int, error) {
	if err := p.Consume(TokenTypeDigit, "expected valid digit for number"); err != nil {
		return 0, err
	}

	number := ByteToInt(p.previousToken.Value)

	for {
		if ok, err := p.Matches(TokenTypeDigit); err != nil {
			return 0, err
		} else if ok {
			number *= 10
			number += ByteToInt(p.previousToken.Value)
		} else {
			break
		}
	}

	return number, nil
}

// ParseNumberN parses a non decimal with N digits.
func (p *Parser) ParseNumberN(n int) (int, error) {
	if n == 0 {
		return 0, p.MakeError("requested ParserNumberN with 0 length number")
	}

	if err := p.Consume(TokenTypeDigit, "expected valid digit for number"); err != nil {
		return 0, err
	}

	number := ByteToInt(p.previousToken.Value)

	for i := 0; i < n-1; i++ {
		if ok, err := p.Matches(TokenTypeDigit); err != nil {
			return 0, err
		} else if ok {
			number *= 10
			number += ByteToInt(p.previousToken.Value)
		} else {
			break
		}
	}

	return number, nil
}

func (p *Parser) ParseAtom() (string, error) {
	if err := p.ConsumeWith(IsAtomChar, "Invalid character detected in atom"); err != nil {
		return "", err
	}

	atom, err := p.CollectBytesWhileMatchesWithPrevWith(IsAtomChar)
	if err != nil {
		return "", err
	}

	return string(atom.Value), nil
}

// Check if the next token matches the given input.
func (p *Parser) Check(tokenType TokenType) bool {
	return p.currentToken.TType == tokenType
}

// CheckWith checks if the next token matches the given condition.
func (p *Parser) CheckWith(f func(tokenType TokenType) bool) bool {
	return f(p.currentToken.TType)
}

// ConsumeNewLine issues two Consume calls for the `CRLF` token sequence.
func (p *Parser) ConsumeNewLine() error {
	if err := p.Consume(TokenTypeCR, "expected CR"); err != nil {
		return err
	}

	if err := p.Consume(TokenTypeLF, "expected LF after CR"); err != nil {
		return err
	}

	return nil
}

// Consume will advance the scanner to the next token if the current token matches the given token. If current
// token does not match, an error with given message will be returned.
func (p *Parser) Consume(tokenType TokenType, message string) error {
	return p.ConsumeWith(func(token TokenType) bool {
		return token == tokenType
	}, message)
}

// ConsumeWith will advance the scanner to the next token if the current token matches the given condition. If current
// token does not match, an error with given message will be returned.
func (p *Parser) ConsumeWith(f func(token TokenType) bool, message string) error {
	if f(p.currentToken.TType) {
		return p.Advance()
	}

	return p.MakeError(message)
}

// ConsumeBytes will advance if the next token value matches the given sequence.
func (p *Parser) ConsumeBytes(chars ...byte) error {
	for _, c := range chars {
		if p.currentToken.Value != c {
			return p.MakeError(fmt.Sprintf("expected byte value %x", c))
		}

		if err := p.Advance(); err != nil {
			return err
		}
	}

	return nil
}

// ConsumeBytesFold behaves the same as ConsumeBytes, but case insensitive for characters.
func (p *Parser) ConsumeBytesFold(chars ...byte) error {
	for _, c := range chars {
		if ByteToLower(p.currentToken.Value) != ByteToLower(c) {
			return p.MakeError(fmt.Sprintf("expected byte value %x", c))
		}

		if err := p.Advance(); err != nil {
			return err
		}
	}

	return nil
}

// MatchesWith will advance the scanner to the next token and return true if the current token matches the given
// condition.
func (p *Parser) MatchesWith(f func(tokenType TokenType) bool) (bool, error) {
	if !p.CheckWith(f) {
		return false, nil
	}

	err := p.Advance()

	return true, err
}

// Matches will advance the scanner to the next token and return true if the current token matches the given tokenType.
func (p *Parser) Matches(tokenType TokenType) (bool, error) {
	if !p.Check(tokenType) {
		return false, nil
	}

	err := p.Advance()

	return true, err
}

// Advance advances the scanner to the next token.
func (p *Parser) Advance() error {
	p.previousToken = p.currentToken

	nextToken, err := p.scanner.ScanToken()
	if err != nil {
		return err
	}

	p.currentToken = nextToken

	return nil
}

// CollectBytesWhileMatchesWithPrev collects bytes from the token scanner while tokens match the given token type.
// This function INCLUDES the previous token consumed before this call.
func (p *Parser) CollectBytesWhileMatchesWithPrev(tokenType TokenType) (Bytes, error) {
	return p.CollectBytesWhileMatchesWithPrevWith(func(tt TokenType) bool {
		return tt == tokenType
	})
}

// CollectBytesWhileMatchesWithPrevWith collects bytes from the token scanner while tokens match the given condition.
// This function INCLUDES the previous token consumed before this call.
func (p *Parser) CollectBytesWhileMatchesWithPrevWith(f func(tokenType TokenType) bool) (Bytes, error) {
	value := []byte{p.previousToken.Value}
	startOffset := p.previousToken.Offset

	for {
		if ok, err := p.MatchesWith(f); err != nil {
			return Bytes{}, err
		} else if ok {
			value = append(value, p.previousToken.Value)
		} else {
			break
		}
	}

	return Bytes{Value: value, Offset: startOffset}, nil
}

// CollectBytesWhileMatches collects bytes from the token scanner while tokens match the given token type. This function
// DOES NOT INCLUDE the previous token consumed before this call.
func (p *Parser) CollectBytesWhileMatches(tokenType TokenType) (Bytes, error) {
	return p.CollectBytesWhileMatchesWith(func(tt TokenType) bool {
		return tt == tokenType
	})
}

// CollectBytesWhileMatchesWith collects bytes from the token scanner while tokens match the given condition. This
// function DOES NOT INCLUDE the previous token consumed before this call.
func (p *Parser) CollectBytesWhileMatchesWith(f func(tokenType TokenType) bool) (Bytes, error) {
	var value []byte

	startOffset := p.currentToken.Offset

	for {
		if ok, err := p.MatchesWith(f); err != nil {
			return Bytes{}, err
		} else if ok {
			value = append(value, p.previousToken.Value)
		} else {
			break
		}
	}

	return Bytes{Value: value, Offset: startOffset}, nil
}

// ResetOffsetCounter resets the token offset back to 0.
func (p *Parser) ResetOffsetCounter() {
	p.scanner.ResetOffsetCounter()
}

func (p *Parser) PreviousToken() Token {
	return p.previousToken
}

func (p *Parser) CurrentToken() Token {
	return p.currentToken
}

func (p *Parser) MakeError(err string) error {
	return &Error{
		Token:   p.previousToken,
		Message: err,
	}
}

func (p *Parser) MakeErrorAtOffset(err string, offset int) error {
	return &Error{
		Token: Token{
			TType:  TokenTypeError,
			Offset: offset,
		},
		Message: err,
	}
}

// SaveState saves the current and previous token state so it can potentially be restored later with RestoreState.
func (p *Parser) SaveState() ParserState {
	return ParserState{
		prevToken: p.previousToken,
		curToken:  p.currentToken,
	}
}

// RestoreState restores the previous and current tokens from the given state.
// NOTE: If this is called without adjusting the scanner input to the location where these were recorded
// you can break your parsing.
func (p *Parser) RestoreState(state ParserState) {
	p.previousToken = state.prevToken
	p.currentToken = state.curToken
}

func IsAStringChar(tokenType TokenType) bool {
	/*
		ASTRING-CHAR   = ATOM-CHAR / resp-specials
	*/
	return IsAtomChar(tokenType) || IsRespSpecial(tokenType)
}

func IsAtomChar(tokenType TokenType) bool {
	/*
		ATOM-CHAR       = <any CHAR except atom-specials>

		atom-specials   = "(" / ")" / "{" / SP / CTL / list-wildcards /
		                  quoted-specials / resp-specials
	*/
	switch tokenType { //nolint:exhaustive
	case TokenTypeLParen:
		fallthrough
	case TokenTypeRParen:
		fallthrough
	case TokenTypeLBracket:
		fallthrough
	case TokenTypeEOF:
		fallthrough
	case TokenTypeSP:
		return false
	}

	return !IsQuotedSpecial(tokenType) && !IsRespSpecial(tokenType) && !IsCTL(tokenType)
}

func IsQuotedSpecial(tokenType TokenType) bool {
	return tokenType == TokenTypeDQuote || tokenType == TokenTypeBackslash
}

func IsRespSpecial(tokenType TokenType) bool {
	return tokenType == TokenTypeRBracket
}

func IsQuotedChar(tokenType TokenType) bool {
	return !IsEOF(tokenType) && !IsQuotedSpecial(tokenType)
}

func IsCTL(tokenType TokenType) bool {
	return tokenType == TokenTypeCTL || tokenType == TokenTypeCR || tokenType == TokenTypeLF || tokenType == TokenTypeTab
}

func ByteToInt(b byte) int {
	return int(b) - int(byte('0'))
}

func IsEOF(tokenType TokenType) bool {
	return tokenType == TokenTypeEOF
}
