package token

// LowestPrec is returned for any Kind that is not a binary operator.
const LowestPrec = 0

// Precedence gives the binding power of a binary operator for precedence
// climbing. Higher binds tighter.
//
// Two operators need care:
//
//   - GTR reports relational precedence, because that is what a lone `>` is.
//     A parser at a shift position must call Join first and use the precedence
//     of the joined Kind; otherwise `a > > b`-adjacent parses as a comparison.
//   - INSTANCEOF shares the relational level but takes a ReferenceType or a
//     Pattern on its right, not an expression, so precedence climbing has to
//     break out of the operand loop for it.
//
// Assignment, `? :` and lambda are right-associative and are not driven by this
// table.
func (k Kind) Precedence() int {
	switch k {
	case LOR:
		return 1
	case LAND:
		return 2
	case OR:
		return 3
	case XOR:
		return 4
	case AND:
		return 5
	case EQL, NEQ:
		return 6
	case LSS, GTR, LEQ, GEQ, INSTANCEOF:
		return 7
	case SHL, SHR, USHR:
		return 8
	case ADD, SUB:
		return 9
	case MUL, QUO, REM:
		return 10
	}
	return LowestPrec
}