package chaincfg

import (
	"math/big"
	"time"

	"github.com/Pingancoin/pacd/internal/wire"
)

const (
	Coin = int64(100_000_000)

	GenesisMessage = "Pingancoin PAC genesis: pure PoW, no premine, BLAKE-256 r14, 2026-06-01"

	PlaceholderProjectPayoutScript = "PAC_MAINNET_3_OF_5_PROJECT_MULTISIG_SCRIPT_REPLACE_BEFORE_LAUNCH"

	MainNetProjectPayoutAddress   = "PskF63mRuFR7krPBwjKHd3FDvbMpjJaNUu"
	MainNetProjectRedeemScriptHex = "532102239e7696e4c9386dfd9a9e4896aff47118aac9f2dffd17d62ab85b78ad0ae0d521024a60aa647fcae613dffb1ee80997e51524fb6cd0086c87bc4385eb4db147568721021fc64f5d87aba44c08153313c1a3c2590e4fd3f346ab68b0eca7407658471ac42103a7b2cad4d103a4270fd23dd843378eeaa98c8f98e7559cc319b9d9e0aece80c22102665657864e3e43aa90db3919e2c4b2b1650d43631d4a950fbfade999f1b4561355ae"
)

var MainNetProjectPayoutScript = []byte{
	0xa9, 0x14, 0xd9, 0x64, 0x6f, 0x55, 0xfc, 0x72,
	0xd3, 0x61, 0x2a, 0x79, 0x10, 0xfa, 0x86, 0x1c,
	0xaf, 0x83, 0xdb, 0x82, 0x81, 0x55, 0x87,
}

type Params struct {
	Name                 string
	Ticker               string
	DefaultPort          string
	NetworkMagic         uint32
	DNSSeeds             []string
	AddressPrefix        string
	PubKeyHashAddrID     byte
	ScriptHashAddrID     byte
	GenesisBlock         *wire.MsgBlock
	GenesisHash          wire.Hash
	PowLimit             *big.Int
	PowLimitBits         uint32
	GenesisBits          uint32
	TargetTimePerBlock   time.Duration
	ASERTHalfLife        time.Duration
	BaseSubsidy          int64
	MulSubsidy           int64
	DivSubsidy           int64
	ReductionInterval    int64
	CoinbaseMaturity     uint32
	MinerRewardPercent   int64
	ProjectRewardPercent int64
	ProjectMultisigM     int
	ProjectMultisigN     int
	ProjectPayoutScript  []byte
}

func MainNetParams() *Params {
	params := commonParams("mainnet", "P", "9508", 0xfacec001, 0x37, 0x38, 0x1d00ffff, 0x1b01ffff, 224)
	params.CoinbaseMaturity = 100
	params.DNSSeeds = []string{
		"server1.pingancoin.org",
		"server2.pingancoin.org",
		"server3.pingancoin.org",
		"server4.pingancoin.org",
	}
	params.ProjectPayoutScript = append([]byte(nil), MainNetProjectPayoutScript...)
	return params
}

func MainNetProjectPayoutIsPlaceholder(params *Params) bool {
	return string(params.ProjectPayoutScript) == PlaceholderProjectPayoutScript
}

func TestNetParams() *Params {
	params := commonParams("testnet", "T", "19508", 0xfacec0a1, 0x41, 0x42, 0x207fffff, 0x207fffff, 255)
	params.CoinbaseMaturity = 100
	params.ProjectPayoutScript = []byte("PAC_TESTNET_3_OF_5_PROJECT_MULTISIG_SCRIPT")
	return params
}

func SimNetParams() *Params {
	params := commonParams("simnet", "S", "29508", 0xfacec0f1, 0x3f, 0x3f, 0x207fffff, 0x207fffff, 255)
	params.ASERTHalfLife = 10 * time.Minute
	params.CoinbaseMaturity = 2
	params.ProjectPayoutScript = []byte("PAC_SIMNET_3_OF_5_PROJECT_MULTISIG_SCRIPT")
	return params
}

func commonParams(name, addressPrefix, defaultPort string, networkMagic uint32, pubKeyHashAddrID, scriptHashAddrID byte, powLimitBits, genesisBits uint32, powLimitShift uint) *Params {
	powLimit := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), powLimitShift), big.NewInt(1))
	genesisBlock := genesisBlock(genesisBits)

	params := &Params{
		Name:                 name,
		Ticker:               "PAC",
		DefaultPort:          defaultPort,
		NetworkMagic:         networkMagic,
		AddressPrefix:        addressPrefix,
		PubKeyHashAddrID:     pubKeyHashAddrID,
		ScriptHashAddrID:     scriptHashAddrID,
		GenesisBlock:         genesisBlock,
		PowLimit:             powLimit,
		PowLimitBits:         powLimitBits,
		GenesisBits:          genesisBits,
		TargetTimePerBlock:   150 * time.Second,
		ASERTHalfLife:        2 * time.Hour,
		BaseSubsidy:          1_692_065_961,
		MulSubsidy:           100,
		DivSubsidy:           101,
		ReductionInterval:    12_288,
		CoinbaseMaturity:     100,
		MinerRewardPercent:   95,
		ProjectRewardPercent: 5,
		ProjectMultisigM:     3,
		ProjectMultisigN:     5,
	}
	params.GenesisHash = genesisBlock.MustBlockHash()
	return params
}

func genesisBlock(bits uint32) *wire.MsgBlock {
	genesisTx := wire.NewCoinbaseTx(0, GenesisMessage, []*wire.TxOut{{
		Value:    0,
		PkScript: []byte(GenesisMessage),
	}})

	block := &wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:   1,
			PrevBlock: wire.ZeroHash(),
			Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Unix(),
			Bits:      bits,
			Nonce:     0,
			Height:    0,
		},
		Transactions: []*wire.MsgTx{genesisTx},
	}
	if err := block.RefreshMerkleRoot(); err != nil {
		panic(err)
	}
	return block
}
